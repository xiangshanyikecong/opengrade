package barcode

import (
	"testing"
)

// withQuiet 在模块序列首尾加静区并放大到多像素/模块,模拟一条真实扫描线。
func withQuiet(mods []bool, scale int) []bool {
	line := make([]bool, 0, (len(mods)+20)*scale)
	for i := 0; i < 10*scale; i++ {
		line = append(line, false)
	}
	for _, m := range mods {
		for k := 0; k < scale; k++ {
			line = append(line, m)
		}
	}
	for i := 0; i < 10*scale; i++ {
		line = append(line, false)
	}
	return line
}

func TestRoundTrip(t *testing.T) {
	contents := []string{
		"0", "9", "42", "1234",                         // Code C(偶位数字)
		"12345", "00000001", "999999999999", "0123456", // Code B(奇位数字)/Code C
		"A", "AZ", "HELLO WORLD", "a-z_09",             // Code B 字符集
	}
	for _, s := range contents {
		mods, err := Encode(s)
		if err != nil {
			t.Fatalf("Encode(%q): %v", s, err)
		}
		for _, scale := range []int{3, 5, 8} {
			got, err := Decode(withQuiet(mods, scale))
			if err != nil {
				t.Fatalf("Decode(%q, scale=%d): %v", s, scale, err)
			}
			if got != s {
				t.Fatalf("roundtrip %q scale=%d 得到 %q", s, scale, got)
			}
		}
	}
}

func TestEncodeErrors(t *testing.T) {
	if _, err := Encode(""); err == nil {
		t.Fatal("空内容应报错")
	}
	if _, err := Encode("中文"); err == nil {
		t.Fatal("非 ASCII 应报错")
	}
}

// TestCodeCCompact 验证偶位数字走 Code C:模块数显著短于 Code B。
func TestCodeCCompact(t *testing.T) {
	mC, _ := Encode("12345678")
	mB, _ := Encode("1234567") // 奇位只能 Code B
	if len(mC) >= len(mB) {
		t.Fatalf("Code C 应更紧凑:%d vs %d", len(mC), len(mB))
	}
}

// TestChecksumReject 篡改校验符后必须解码失败(mod-103 拦截)。
func TestChecksumReject(t *testing.T) {
	mods, _ := Encode("12345678")
	// 找到校验符区域(终止符前 11 模块),整体取反一次即可改变该符号
	// 终止符 13 模块,校验符在它前面 11 模块
	start := len(mods) - 13 - 11
	line := withQuiet(mods, 5)
	// 翻转校验符中一个条模块对应的 5 个像素
	for i := start * 5; i < start*5+5; i++ {
		line[i] = !line[i]
	}
	if _, err := Decode(line); err == nil {
		t.Fatal("篡改校验符后仍解码成功,校验失效")
	}
}

// TestNoiseTolerance 边缘静区混入少量噪声不应影响解码(首尾游程被裁剪)。
func TestNoiseTolerance(t *testing.T) {
	mods, _ := Encode("00420013")
	line := withQuiet(mods, 6)
	line[3] = true  // 左静区孤立噪点
	line[7] = true  // 相邻再补一个,凑成 2px 黑斑
	line[len(line)-2] = true // 右静区噪点
	got, err := Decode(line)
	if err != nil {
		t.Fatalf("静区噪声导致解码失败:%v", err)
	}
	if got != "00420013" {
		t.Fatalf("得到 %q", got)
	}
}

func TestAllWidthsSum11(t *testing.T) {
	for v := 0; v < 106; v++ {
		w := widths[v]
		sum := 0
		for _, x := range w {
			sum += x
		}
		if sum != 11 {
			t.Fatalf("符号 %d 模块总数 %d ≠ 11", v, sum)
		}
	}
	sum := 0
	for _, x := range stopPattern {
		sum += x
	}
	if sum != 13 {
		t.Fatalf("终止符模块总数 %d ≠ 13", sum)
	}
	// 反查表无冲突(107 个符号宽度两两不同)
	if len(widthsIndex) != 107 {
		t.Fatalf("宽度反查表大小 %d ≠ 107,存在重复宽度组合", len(widthsIndex))
	}
}

func TestDecodeGarbage(t *testing.T) {
	allBlack := make([]bool, 120)
	for i := range allBlack {
		allBlack[i] = true
	}
	for _, line := range [][]bool{nil, make([]bool, 200), allBlack} {
		if _, err := Decode(line); err == nil {
			t.Fatal("无效输入不应解码成功")
		}
	}
}
