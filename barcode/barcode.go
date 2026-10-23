// Package barcode 实现 Code 128 条形码的编码与解码。
// 码表与 mod-103 校验遵循 ISO/IEC 15417;纯 Go、零第三方依赖。
//
// 用途:答题卡考号条码通道——card 用 Encode 印染黑白条(共享几何,读写同源),
// omr 在透视矫正后的标准网格上取多条水平扫描线分别 Decode 后投票。
// 解码只面向正确方向(版面已由四角定位块矫正),不做反向/镜像识别。
package barcode

import (
	"fmt"
	"strings"
)

// widths 是 107 个符号的模块宽度表,顺序为 [条,空,条,空,条,空],每个符号恒 11 模块。
// 逐行抄录自 ISO/IEC 15417 标准码表(行尾注释即 6 位宽度串,便于人工比对)。
// 值 0~94 对应 Code B 字符(ASCII 32~126),95~102 为功能符,103~105 为起始符,106 为终止符。
var widths = [107][6]int{
	/*  0 */ {2, 1, 2, 2, 2, 2}, // 212222
	/*  1 */ {2, 2, 2, 1, 2, 2}, // 222122
	/*  2 */ {2, 2, 2, 2, 2, 1}, // 222221
	/*  3 */ {1, 2, 1, 2, 2, 3}, // 121223
	/*  4 */ {1, 2, 1, 3, 2, 2}, // 121322
	/*  5 */ {1, 3, 1, 2, 2, 2}, // 131222
	/*  6 */ {1, 2, 2, 2, 1, 3}, // 122213
	/*  7 */ {1, 2, 2, 3, 1, 2}, // 122312
	/*  8 */ {1, 3, 2, 2, 1, 2}, // 132212
	/*  9 */ {2, 2, 1, 2, 1, 3}, // 221213
	/* 10 */ {2, 2, 1, 3, 1, 2}, // 221312
	/* 11 */ {2, 3, 1, 2, 1, 2}, // 231212
	/* 12 */ {1, 1, 2, 2, 3, 2}, // 112232
	/* 13 */ {1, 2, 2, 1, 3, 2}, // 122132
	/* 14 */ {1, 2, 2, 2, 3, 1}, // 122231
	/* 15 */ {1, 1, 3, 2, 2, 2}, // 113222
	/* 16 */ {1, 2, 3, 1, 2, 2}, // 123122
	/* 17 */ {1, 2, 3, 2, 2, 1}, // 123221
	/* 18 */ {2, 2, 3, 2, 1, 1}, // 223211
	/* 19 */ {2, 2, 1, 1, 3, 2}, // 221132
	/* 20 */ {2, 2, 1, 2, 3, 1}, // 221231
	/* 21 */ {2, 1, 3, 2, 1, 2}, // 213212
	/* 22 */ {2, 2, 3, 1, 1, 2}, // 223112
	/* 23 */ {3, 1, 2, 1, 3, 1}, // 312131
	/* 24 */ {3, 1, 1, 2, 2, 2}, // 311222
	/* 25 */ {3, 2, 1, 1, 2, 2}, // 321122
	/* 26 */ {3, 2, 1, 2, 2, 1}, // 321221
	/* 27 */ {3, 1, 2, 2, 1, 2}, // 312212
	/* 28 */ {3, 2, 2, 1, 1, 2}, // 322112
	/* 29 */ {3, 2, 2, 2, 1, 1}, // 322211
	/* 30 */ {2, 1, 2, 1, 2, 3}, // 212123
	/* 31 */ {2, 1, 2, 3, 2, 1}, // 212321
	/* 32 */ {2, 3, 2, 1, 2, 1}, // 232121
	/* 33 */ {1, 1, 1, 3, 2, 3}, // 111323
	/* 34 */ {1, 3, 1, 1, 2, 3}, // 131123
	/* 35 */ {1, 3, 1, 3, 2, 1}, // 131321
	/* 36 */ {1, 1, 2, 3, 1, 3}, // 112313
	/* 37 */ {1, 3, 2, 1, 1, 3}, // 132113
	/* 38 */ {1, 3, 2, 3, 1, 1}, // 132311
	/* 39 */ {2, 1, 1, 3, 1, 3}, // 211313
	/* 40 */ {2, 3, 1, 1, 1, 3}, // 231113
	/* 41 */ {2, 3, 1, 3, 1, 1}, // 231311
	/* 42 */ {1, 1, 2, 1, 3, 3}, // 112133
	/* 43 */ {1, 1, 2, 3, 3, 1}, // 112331
	/* 44 */ {1, 3, 2, 1, 3, 1}, // 132131
	/* 45 */ {1, 1, 3, 1, 2, 3}, // 113123
	/* 46 */ {1, 1, 3, 3, 2, 1}, // 113321
	/* 47 */ {1, 3, 3, 1, 2, 1}, // 133121
	/* 48 */ {3, 1, 3, 1, 2, 1}, // 313121
	/* 49 */ {2, 1, 1, 3, 3, 1}, // 211331
	/* 50 */ {2, 3, 1, 1, 3, 1}, // 231131
	/* 51 */ {2, 1, 3, 1, 1, 3}, // 213113
	/* 52 */ {2, 1, 3, 3, 1, 1}, // 213311
	/* 53 */ {2, 1, 3, 1, 3, 1}, // 213131
	/* 54 */ {3, 1, 1, 1, 2, 3}, // 311123
	/* 55 */ {3, 1, 1, 3, 2, 1}, // 311321
	/* 56 */ {3, 3, 1, 1, 2, 1}, // 331121
	/* 57 */ {3, 1, 2, 1, 1, 3}, // 312113
	/* 58 */ {3, 1, 2, 3, 1, 1}, // 312311
	/* 59 */ {3, 3, 2, 1, 1, 1}, // 332111
	/* 60 */ {3, 1, 4, 1, 1, 1}, // 314111
	/* 61 */ {2, 2, 1, 4, 1, 1}, // 221411
	/* 62 */ {4, 3, 1, 1, 1, 1}, // 431111
	/* 63 */ {1, 1, 1, 2, 2, 4}, // 111224
	/* 64 */ {1, 1, 1, 4, 2, 2}, // 111422
	/* 65 */ {1, 2, 1, 1, 2, 4}, // 121124
	/* 66 */ {1, 2, 1, 4, 2, 1}, // 121421
	/* 67 */ {1, 4, 1, 1, 2, 2}, // 141122
	/* 68 */ {1, 4, 1, 2, 2, 1}, // 141221
	/* 69 */ {1, 1, 2, 2, 1, 4}, // 112214
	/* 70 */ {1, 1, 2, 4, 1, 2}, // 112412
	/* 71 */ {1, 2, 2, 1, 1, 4}, // 122114
	/* 72 */ {1, 2, 2, 4, 1, 1}, // 122411
	/* 73 */ {1, 4, 2, 1, 1, 2}, // 142112
	/* 74 */ {1, 4, 2, 2, 1, 1}, // 142211
	/* 75 */ {2, 4, 1, 2, 1, 1}, // 241211
	/* 76 */ {2, 2, 1, 1, 1, 4}, // 221114
	/* 77 */ {4, 1, 3, 1, 1, 1}, // 413111
	/* 78 */ {2, 4, 1, 1, 1, 2}, // 241112
	/* 79 */ {1, 3, 4, 1, 1, 1}, // 134111
	/* 80 */ {1, 1, 1, 2, 4, 2}, // 111242
	/* 81 */ {1, 2, 1, 1, 4, 2}, // 121142
	/* 82 */ {1, 2, 1, 2, 4, 1}, // 121241
	/* 83 */ {1, 1, 4, 2, 1, 2}, // 114212
	/* 84 */ {1, 2, 4, 1, 1, 2}, // 124112
	/* 85 */ {1, 2, 4, 2, 1, 1}, // 124211
	/* 86 */ {4, 1, 1, 2, 1, 2}, // 411212
	/* 87 */ {4, 2, 1, 1, 1, 2}, // 421112
	/* 88 */ {4, 2, 1, 2, 1, 1}, // 421211
	/* 89 */ {2, 1, 2, 1, 4, 1}, // 212141
	/* 90 */ {2, 1, 4, 1, 2, 1}, // 214121
	/* 91 */ {4, 1, 2, 1, 2, 1}, // 412121
	/* 92 */ {1, 1, 1, 1, 4, 3}, // 111143
	/* 93 */ {1, 1, 1, 3, 4, 1}, // 111341
	/* 94 */ {1, 3, 1, 1, 4, 1}, // 131141
	/* 95 */ {1, 1, 4, 1, 1, 3}, // 114113
	/* 96 */ {1, 1, 4, 3, 1, 1}, // 114311
	/* 97 */ {4, 1, 1, 1, 1, 3}, // 411113
	/* 98 */ {4, 1, 1, 3, 1, 1}, // 411311
	/* 99 */ {1, 1, 3, 1, 4, 1}, // 113141
	/*100 */ {1, 1, 4, 1, 3, 1}, // 114131
	/*101 */ {3, 1, 1, 1, 4, 1}, // 311141
	/*102 */ {4, 1, 1, 1, 3, 1}, // 411131
	/*103 */ {2, 1, 1, 4, 1, 2}, // 211412 Start A
	/*104 */ {2, 1, 1, 2, 1, 4}, // 211214 Start B
	/*105 */ {2, 1, 1, 2, 3, 2}, // 211232 Start C
}

// stopPattern 终止符宽度:7 元素、13 模块,末条 2 模块是终止条。
var stopPattern = [7]int{2, 3, 3, 1, 1, 1, 2} // 2331112

const (
	startA  = 103
	startB  = 104
	startC  = 105
	switchC = 99  // Code B 中途切到 Code C
	switchB = 100 // Code C 中途切到 Code B
	stopSym = 106
)

// widthsIndex 反查表:宽度元组 → 符号值。
var widthsIndex = func() map[[6]int]int {
	m := make(map[[6]int]int, 107)
	for v, w := range widths {
		m[w] = v
	}
	return m
}()

// Encode 把内容编码为 Code 128 模块序列(true=条/黑)。返回序列长度即模块总数,
// 渲染方据此按可用宽度取整模块像素。内容为纯数字且位数为偶时用 Code C
// (两位数字一个符号,条码更短、模块更宽、更耐扫描损伤),否则用 Code B(ASCII 32~126)。
func Encode(content string) ([]bool, error) {
	if content == "" {
		return nil, fmt.Errorf("条码内容为空")
	}
	var values []int
	if allDigits(content) && len(content)%2 == 0 {
		values = append(values, startC)
		for i := 0; i+1 < len(content); i += 2 {
			values = append(values, int(content[i]-'0')*10+int(content[i+1]-'0'))
		}
	} else {
		values = append(values, startB)
		for _, r := range content {
			if r < 32 || r > 126 {
				return nil, fmt.Errorf("Code B 仅支持 ASCII 32~126,遇到 %q", r)
			}
			values = append(values, int(r)-32)
		}
	}
	// 校验符:起始符权 1,其后第 i 个符号权 i(切换符也计入),mod 103
	sum := 0
	for i, v := range values {
		if i == 0 {
			sum += v
		} else {
			sum += v * i
		}
	}
	values = append(values, sum%103)

	mods := make([]bool, 0, len(values)*11+13)
	ink := true
	emit := func(w int) {
		for k := 0; k < w; k++ {
			mods = append(mods, ink)
		}
		ink = !ink
	}
	for _, v := range values {
		ink = true
		for _, w := range widths[v] {
			emit(w)
		}
	}
	ink = true
	for _, w := range stopPattern {
		emit(w)
	}
	return mods, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// run 是扫描线上的一段同色游程。
type run struct {
	w     int
	black bool
}

func runsOf(line []bool) []run {
	var out []run
	for i := 0; i < len(line); {
		j := i
		for j < len(line) && line[j] == line[i] {
			j++
		}
		out = append(out, run{w: j - i, black: line[i]})
		i = j
	}
	return out
}

// Decode 在一条二值扫描线上解码(true=黑/条)。容忍首尾静区与少量边缘噪点:
// 逐符号按自身总宽归一化模块宽度(自同步),经 mod-103 校验;失败时剥离
// 首部/尾部噪点游程(各最多 2 个黑游程)重试。仍失败则返回错误——
// 调用方对多条扫描线投票取众数。
func Decode(line []bool) (string, error) {
	runs := runsOf(line)
	var lastErr error
	for lead := 0; lead <= 4; lead += 2 {
		for tail := 0; tail <= 2; tail += 2 {
			r := trimEdges(runs, lead, tail)
			if len(r) < 19 { // start+check+stop 的最小游程规模
				continue
			}
			s, err := decodeRuns(r)
			if err == nil {
				return s, nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("扫描线上没有可识别的条码")
	}
	return "", lastErr
}

// trimEdges 去首尾空白游程后,额外剥离 lead 个首部黑游程(连同其后空白)
// 与 tail 个尾部黑游程(连同其前空白)。游程交替,成对剥离后仍以黑起止。
func trimEdges(runs []run, lead, tail int) []run {
	out := runs
	if len(out) > 0 && !out[0].black {
		out = out[1:]
	}
	if len(out) > 0 && !out[len(out)-1].black {
		out = out[:len(out)-1]
	}
	for ; lead > 0 && len(out) >= 2; lead-- {
		out = out[2:]
	}
	for ; tail > 0 && len(out) >= 2; tail-- {
		out = out[:len(out)-2]
	}
	return out
}

// decodeRuns 解析以黑游程起始、黑游程终止的完整游程序列。
func decodeRuns(runs []run) (string, error) {
	if !runs[0].black || !runs[len(runs)-1].black {
		return "", fmt.Errorf("游程序列应以条起止")
	}
	start, err := symbolAt(runs, 0)
	if err != nil {
		return "", err
	}
	if start != startB && start != startC {
		return "", fmt.Errorf("起始符 %d 不支持(仅 Code B/C)", start)
	}
	values := []int{start} // decodeValues 按约定接收 [起始符, 数据…, 校验符]
	pos := 6
	for pos < len(runs) {
		if _, ok := stopAt(runs, pos); ok {
			return decodeValues(values)
		}
		v, err := symbolAt(runs, pos)
		if err != nil {
			return "", err
		}
		values = append(values, v)
		pos += 6
	}
	return "", fmt.Errorf("未找到终止符")
}

// symbolAt 把 runs[i:i+6] 解析为一个符号值(按 6 游程总宽 11 模块归一化)。
func symbolAt(runs []run, i int) (int, error) {
	if i+6 > len(runs) {
		return 0, fmt.Errorf("游程不足:解析符号需要 6 段,剩余 %d", len(runs)-i)
	}
	total := 0
	for k := i; k < i+6; k++ {
		total += runs[k].w
	}
	unit := float64(total) / 11.0
	var key [6]int
	qsum := 0
	for k := 0; k < 6; k++ {
		q := int(float64(runs[i+k].w)/unit + 0.5)
		if q < 1 || q > 4 {
			return 0, fmt.Errorf("模块宽度量化越界(%d)", q)
		}
		key[k] = q
		qsum += q
	}
	if qsum != 11 {
		return 0, fmt.Errorf("符号模块总数 %d ≠ 11", qsum)
	}
	v, ok := widthsIndex[key]
	if !ok {
		return 0, fmt.Errorf("未知宽度组合 %v", key)
	}
	return v, nil
}

// stopAt 判断 runs[i:] 是否为终止符(7 游程 13 模块)。
func stopAt(runs []run, i int) (int, bool) {
	if i+7 > len(runs) {
		return 0, false
	}
	total := 0
	for k := i; k < i+7; k++ {
		total += runs[k].w
	}
	unit := float64(total) / 13.0
	qsum := 0
	for k := 0; k < 7; k++ {
		q := int(float64(runs[i+k].w)/unit + 0.5)
		if q != stopPattern[k] {
			return 0, false
		}
		qsum += q
	}
	if qsum != 13 {
		return 0, false
	}
	return stopSym, true
}

// decodeValues 校验并按码制还原符号流为文本(Code B/C,支持中途切换)。
// values 含起始符、数据与切换符号,末位是校验符(不含终止符)。
func decodeValues(values []int) (string, error) {
	if len(values) < 2 {
		return "", fmt.Errorf("符号数不足(缺校验符)")
	}
	sum := values[0]
	for i := 1; i < len(values)-1; i++ {
		sum += values[i] * i
	}
	if sum%103 != values[len(values)-1] {
		return "", fmt.Errorf("校验符不符(期望 %d,读到 %d)", sum%103, values[len(values)-1])
	}
	var sb strings.Builder
	codeB := values[0] == startB
	for i := 1; i < len(values)-1; i++ {
		v := values[i]
		switch {
		case codeB:
			switch {
			case v == switchC:
				codeB = false
			case v < 95:
				sb.WriteByte(byte(v + 32))
			default:
				return "", fmt.Errorf("Code B 中不支持的功能符 %d", v)
			}
		default: // Code C
			switch {
			case v == switchB:
				codeB = true
			case v < 100:
				sb.WriteByte(byte('0' + v/10))
				sb.WriteByte(byte('0' + v%10))
			default:
				return "", fmt.Errorf("Code C 中不支持的功能符 %d", v)
			}
		}
	}
	return sb.String(), nil
}
