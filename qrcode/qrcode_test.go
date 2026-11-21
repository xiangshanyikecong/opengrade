package qrcode

import (
	"image"
	"math/rand"
	"testing"
)

func TestRoundTripGrid(t *testing.T) {
	// 逐长度扫描:1..34 → v1,35..63 → v2,64..101 → v3,102..149 → v4
	check := func(content string, wantVersion int) {
		t.Helper()
		m, err := EncodeNumeric(content)
		if err != nil {
			t.Fatalf("EncodeNumeric(%d 位): %v", len(content), err)
		}
		if m.Version != wantVersion {
			t.Fatalf("%d 位:版本 %d,期望 %d", len(content), m.Version, wantVersion)
		}
		got, err := DecodeGrid(m.Dark)
		if err != nil {
			t.Fatalf("DecodeGrid(%d 位): %v", len(content), err)
		}
		if got != content {
			t.Fatalf("%d 位 roundtrip 得到 %d 位 %q", len(content), len(got), got)
		}
	}
	for n := 1; n <= 34; n++ {
		check(repeatDigits("0123456789", (n+9)/10)[:n], 1)
	}
	for _, n := range []int{35, 40, 63} {
		check(repeatDigits("0123456789", (n+9)/10)[:n], 2)
	}
	for _, n := range []int{64, 80, 101} {
		check(repeatDigits("9876543210", (n+9)/10)[:n], 3)
	}
	for _, n := range []int{102, 130, 149} {
		check(repeatDigits("1357902468", (n+9)/10)[:n], 4)
	}
}

func repeatDigits(seed string, times int) string {
	out := make([]byte, 0, len(seed)*times)
	for i := 0; i < times; i++ {
		out = append(out, seed...)
	}
	return string(out)
}

// TestRSErrorCorrection 数据模块被污损 ≤ ecLen/2 处时仍可完整恢复。
func TestRSErrorCorrection(t *testing.T) {
	m, err := EncodeNumeric("20260101001")
	if err != nil {
		t.Fatal(err)
	}
	spec := versions[m.Version-1]
	funcMod := buildFuncMap(spec)
	rng := rand.New(rand.NewSource(7))
	flipped := 0
	for flipped < 4 { // v1-M 可纠 5 处,故意留余量
		r, c := rng.Intn(spec.size), rng.Intn(spec.size)
		if funcMod[r][c] {
			continue // 只翻数据模块
		}
		m.Dark[r][c] = !m.Dark[r][c]
		flipped++
	}
	got, err := DecodeGrid(m.Dark)
	if err != nil {
		t.Fatalf("翻转 %d 个数据模块后解码失败:%v", flipped, err)
	}
	if got != "20260101001" {
		t.Fatalf("纠错后得到 %q", got)
	}
}

// TestRSTooManyErrors 错误超过纠错能力必须报错。
func TestRSTooManyErrors(t *testing.T) {
	data := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	msg := append(append([]byte{}, data...), rsEncode(data, 10)...)
	// 破坏 6 处(t=5)
	for i := 0; i < 6; i++ {
		msg[i*2] ^= 0xFF
	}
	if _, err := rsDecode(msg, 10); err == nil {
		t.Fatal("6 处错误超过 v1-M 纠错能力(5),应报错")
	}
}

// TestRSRoundTrip RS 编解码直接闭环(多块长度、逐级损坏)。
func TestRSRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, ecLen := range []int{10, 16, 18, 26} {
		dataLen := 3*ecLen/2 + 7
		data := make([]byte, dataLen)
		for i := range data {
			data[i] = byte(rng.Intn(256))
		}
		msg := append(append([]byte{}, data...), rsEncode(data, ecLen)...)
		fixed, err := rsDecode(msg, ecLen)
		if err != nil {
			t.Fatalf("ecLen=%d 无损解码失败:%v", ecLen, err)
		}
		if string(fixed) != string(data) {
			t.Fatalf("ecLen=%d 无损 roundtrip 不一致", ecLen)
		}
		// 逐级损坏到 t-1 处
		tCap := ecLen / 2
		for n := 1; n < tCap; n++ {
			dirty := append([]byte{}, msg...)
			for i := 0; i < n; i++ {
				dirty[rng.Intn(len(dirty))] ^= byte(1 + rng.Intn(255))
			}
			fixed, err := rsDecode(dirty, ecLen)
			if err != nil {
				t.Fatalf("ecLen=%d 损坏 %d 处纠错失败:%v", ecLen, n, err)
			}
			if string(fixed) != string(data) {
				t.Fatalf("ecLen=%d 损坏 %d 处恢复不一致", ecLen, n)
			}
		}
	}
}

// TestFormatRedundancy 单份格式信息损坏时用另一份解码。
func TestFormatRedundancy(t *testing.T) {
	m, _ := EncodeNumeric("00420013")
	// 翻转 copy1 中的一个格式位:(8,0)
	m.Dark[8][0] = !m.Dark[8][0]
	got, err := DecodeGrid(m.Dark)
	if err != nil {
		t.Fatalf("格式信息单份损坏后解码失败:%v", err)
	}
	if got != "00420013" {
		t.Fatalf("得到 %q", got)
	}
}

// TestRegionDecode 像素级闭环:渲染模块矩阵到灰度图(带静区与确定性噪声),
// 走 DecodeRegion 全链路(定位图形检测 → 网格采样 → 格式信息 → RS 纠错)。
func TestRegionDecode(t *testing.T) {
	for _, content := range []string{"12345678", "00000001", "987654321098", "42"} {
		m, err := EncodeNumeric(content)
		if err != nil {
			t.Fatal(err)
		}
		const scale, quiet = 8, 5 // px/模块、静区模块数
		n := len(m.Dark)
		side := (n + 2*quiet) * scale
		gray := image.NewGray(image.Rect(0, 0, side, side))
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				gray.Pix[y*side+x] = 255
			}
		}
		rng := rand.New(rand.NewSource(int64(len(content))))
		for r := 0; r < n; r++ {
			for c := 0; c < n; c++ {
				if !m.Dark[r][c] {
					continue
				}
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							gray.Pix[((quiet+r)*scale+dy)*side+(quiet+c)*scale+dx] = 0
						}
					}
			}
		}
		// 高斯式噪声(σ≈6)+ 轻度灰度扰动
		for i := range gray.Pix {
			noise := 0
			for k := 0; k < 3; k++ {
				noise += rng.Intn(9) - 4
			}
			v := int(gray.Pix[i]) + noise
			if v < 0 {
				v = 0
			} else if v > 255 {
				v = 255
			}
			gray.Pix[i] = byte(v)
		}
		got, err := DecodeRegion(gray, gray.Rect)
		if err != nil {
			t.Fatalf("DecodeRegion(%q): %v", content, err)
		}
		if got != content {
			t.Fatalf("DecodeRegion(%q) 得到 %q", content, got)
		}
	}
}

// TestEncodeErrors 非数字/空内容必须报错。
func TestEncodeErrors(t *testing.T) {
	if _, err := EncodeNumeric(""); err == nil {
		t.Fatal("空内容应报错")
	}
	if _, err := EncodeNumeric("12A4"); err == nil {
		t.Fatal("非数字应报错")
	}
	if _, err := EncodeNumeric(repeatDigits("1", 150)); err == nil {
		t.Fatal("超出 v4 容量应报错")
	}
}
