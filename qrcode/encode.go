// encode.go:QR 矩阵构建(数字模式、纠错级 M、版本 v1~v4)。
// 输出 Dark[r][c](true=黑模块),card 直接按模块印染,解码端在同尺寸网格上还原。
package qrcode

import "fmt"

// versionSpec 是一个版本的静态参数(纠错级 M)。
type versionSpec struct {
	size      int   // 模块边长
	align     []int // 校正图形中心坐标(v1 无)
	blocks    int   // RS 分块数
	dataCW    int   // 数据码字总数
	blockData int   // 每块数据码字数
	blockEC   int   // 每块纠错码字数
}

// versions 依次为 v1~v4(纠错级 M),参数取自 ISO/IEC 18004 纠错特性表。
var versions = []versionSpec{
	{size: 21, align: nil, blocks: 1, dataCW: 16, blockData: 16, blockEC: 10},
	{size: 25, align: []int{6, 18}, blocks: 1, dataCW: 28, blockData: 28, blockEC: 16},
	{size: 29, align: []int{6, 22}, blocks: 1, dataCW: 44, blockData: 44, blockEC: 26},
	{size: 33, align: []int{6, 26}, blocks: 2, dataCW: 64, blockData: 32, blockEC: 18},
}

// ecLevelM 纠错级 M 的格式信息指示位(00)。
const ecLevelM = 0

// Matrix 是编码结果:模块矩阵(true=黑)+ 版本号。
type Matrix struct {
	Version int
	Dark    [][]bool
}

// EncodeNumeric 把纯数字内容编码为 QR(v1~v4 自动选最小可容版本,纠错级 M,数字模式)。
// v1-M 数字容量 33 位,v4-M 达 127 位;考号(≤12 位)恒用 v1。
func EncodeNumeric(content string) (*Matrix, error) {
	digits, err := numericBits(content)
	if err != nil {
		return nil, err
	}
	vi := -1
	for i, v := range versions {
		if len(digits) <= v.dataCW*8 {
			vi = i
			break
		}
	}
	if vi < 0 {
		return nil, fmt.Errorf("内容 %d 位数字超出 v4-M 容量(127 位)", len(content))
	}
	spec := versions[vi]

	// 码字化:补终止符(最多 4 个 0)与字节边界填充,交替 0xEC/0x11 至满
	bits := append([]bool{}, digits...)
	for i := 0; i < 4 && len(bits) < spec.dataCW*8; i++ {
		bits = append(bits, false)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	data := make([]byte, 0, spec.dataCW)
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8; j++ {
			b = b<<1 | boolByte(bits[i+j])
		}
		data = append(data, b)
	}
	pad := true
	for len(data) < spec.dataCW {
		if pad {
			data = append(data, 0xEC)
		} else {
			data = append(data, 0x11)
		}
		pad = !pad
	}

	// 分块 RS 编码 + 交织(v1~v3 单块直通)
	codewords := interleaveBlocks(spec, data)

	// 逐掩码构建完整矩阵,取惩罚分最低者
	best := -1
	var bestGrid [][]bool
	for mask := 0; mask < 8; mask++ {
		grid := buildMatrix(spec, codewords, mask)
		if best < 0 || penalty(grid) < penalty(bestGrid) {
			best = mask
			bestGrid = grid
		}
	}
	return &Matrix{Version: vi + 1, Dark: bestGrid}, nil
}

// numericBits 生成数字模式位流:4 位模式指示(0001)+ 10 位计数(v1~v9)
// + 3 位一组 10 比特、余 1 位补 4 比特、余 2 位补 7 比特。
func numericBits(content string) ([]bool, error) {
	if content == "" {
		return nil, fmt.Errorf("二维码内容为空")
	}
	for i := 0; i < len(content); i++ {
		if content[i] < '0' || content[i] > '9' {
			return nil, fmt.Errorf("数字模式仅支持 0~9,遇到 %q", content[i])
		}
	}
	bits := make([]bool, 0, 14+len(content)*4)
	push := func(v, n int) {
		for k := n - 1; k >= 0; k-- {
			bits = append(bits, (v>>uint(k))&1 == 1)
		}
	}
	push(1, 4)                 // 模式 0001
	push(len(content), 10)     // 计数(v1~v9 用 10 位)
	for i := 0; i+3 <= len(content); i += 3 {
		v := int(content[i]-'0')*100 + int(content[i+1]-'0')*10 + int(content[i+2]-'0')
		push(v, 10)
	}
	switch len(content) % 3 {
	case 1:
		push(int(content[len(content)-1]-'0'), 4)
	case 2:
		push(int(content[len(content)-2]-'0')*10+int(content[len(content)-1]-'0'), 7)
	}
	return bits, nil
}

// interleaveBlocks 分块计算 RS 纠错码字并按规范交织(数据先、纠错后)。
func interleaveBlocks(spec versionSpec, data []byte) []byte {
	dataBlocks := make([][]byte, spec.blocks)
	ecBlocks := make([][]byte, spec.blocks)
	for b := 0; b < spec.blocks; b++ {
		blk := data[b*spec.blockData : (b+1)*spec.blockData]
		dataBlocks[b] = blk
		ecBlocks[b] = rsEncode(blk, spec.blockEC)
	}
	out := make([]byte, 0, spec.dataCW+spec.blocks*spec.blockEC)
	for i := 0; i < spec.blockData; i++ {
		for b := 0; b < spec.blocks; b++ {
			out = append(out, dataBlocks[b][i])
		}
	}
	for i := 0; i < spec.blockEC; i++ {
		for b := 0; b < spec.blocks; b++ {
			out = append(out, ecBlocks[b][i])
		}
	}
	return out
}

// buildMatrix 放置功能图形 + 格式信息 + 掩码后的数据码字。
func buildMatrix(spec versionSpec, codewords []byte, mask int) [][]bool {
	n := spec.size
	grid := make([][]bool, n)
	for r := range grid {
		grid[r] = make([]bool, n)
	}
	funcMod := buildFuncMap(spec) // true = 功能模块(grid 已涂)

	drawFinders(grid, n)
	drawTiming(grid, n)
	drawAlignment(grid, spec)
	grid[n-8][8] = true // 恒黑模块 (4V+9, 8)
	drawFormat(grid, n, mask)

	// 数据位流(含纠错码字)
	bits := make([]bool, 0, len(codewords)*8)
	for _, b := range codewords {
		for k := 7; k >= 0; k-- {
			bits = append(bits, (b>>uint(k))&1 == 1)
		}
	}
	bitIdx := 0
	up := true
	for col := n - 1; col > 0; col -= 2 {
		if col == 6 {
			col-- // 跳过时序列
		}
		for i := 0; i < n; i++ {
			r := i
			if up {
				r = n - 1 - i
			}
			for c := 0; c < 2; c++ {
				cc := col - c
				if funcMod[r][cc] {
					continue
				}
				v := bitIdx < len(bits) && bits[bitIdx]
				bitIdx++
				if maskBit(mask, r, cc) {
					v = !v
				}
				grid[r][cc] = v
			}
		}
		up = !up
	}
	return grid
}

// buildFuncMap 标记全部功能模块位置(含格式信息两份,不含数据区)。
func buildFuncMap(spec versionSpec) [][]bool {
	n := spec.size
	m := make([][]bool, n)
	for r := range m {
		m[r] = make([]bool, n)
	}
	mark := func(r0, c0, h, w int) {
		for r := r0; r < r0+h; r++ {
			for c := c0; c < c0+w; c++ {
				m[r][c] = true
			}
		}
	}
	// 三个 7×7 定位图形 + 1 模块分隔带
	mark(0, 0, 8, 8)
	mark(0, n-8, 8, 8)
	mark(n-8, 0, 8, 8)
	// 时序列(第 6 行/列)
	mark(6, 0, 1, n)
	mark(0, 6, n, 1)
	// 校正图形 5×5(跳过与定位图形重叠的角)
	for _, ar := range spec.align {
		for _, ac := range spec.align {
			if (ar == 6 || ar == spec.align[len(spec.align)-1]) && (ac == 6 || ac == spec.align[len(spec.align)-1]) {
				continue
			}
			mark(ar-2, ac-2, 5, 5)
		}
	}
	// 格式信息两份 + 恒黑模块
	for i := 0; i <= 5; i++ {
		m[8][i] = true
	}
	m[8][7] = true
	m[8][8] = true
	m[7][8] = true
	for i := 9; i < 15; i++ {
		m[14-i][8] = true
	}
	for i := 0; i < 7; i++ {
		m[n-1-i][8] = true
	}
	for i := 7; i < 15; i++ {
		m[8][n-15+i] = true
	}
	m[n-8][8] = true
	return m
}

func drawFinders(grid [][]bool, n int) {
	// 7×7:外环黑、中环白、中心 3×3 黑;再外圈 1 模块白分隔带(保持白底即可)
	pat := func(r0, c0 int) {
		for r := 0; r < 7; r++ {
			for c := 0; c < 7; c++ {
				ring := r == 0 || r == 6 || c == 0 || c == 6
				core := r >= 2 && r <= 4 && c >= 2 && c <= 4
				grid[r0+r][c0+c] = ring || core
			}
		}
	}
	pat(0, 0)
	pat(0, n-7)
	pat(n-7, 0)
}

func drawTiming(grid [][]bool, n int) {
	for i := 8; i < n-8; i++ {
		grid[6][i] = i%2 == 0
		grid[i][6] = i%2 == 0
	}
}

func drawAlignment(grid [][]bool, spec versionSpec) {
	if len(spec.align) == 0 {
		return // v1 无校正图形
	}
	maxC := spec.align[len(spec.align)-1]
	for _, ar := range spec.align {
		for _, ac := range spec.align {
			if (ar == 6 || ar == maxC) && (ac == 6 || ac == maxC) {
				continue
			}
			for r := -2; r <= 2; r++ {
				for c := -2; c <= 2; c++ {
					ring := r == -2 || r == 2 || c == -2 || c == 2
					grid[ar+r][ac+c] = ring || (r == 0 && c == 0)
				}
			}
		}
	}
}

// drawFormat 计算并放置格式信息(纠错级 M + 掩码号):5 位数据 + BCH(15,5),再异或 0x5412。
func drawFormat(grid [][]bool, n, mask int) {
	data := ecLevelM<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := uint(data<<10|rem) ^ 0x5412
	get := func(i uint) bool { return (bits>>i)&1 == 1 }
	// 第一份(左上)
	for i := 0; i <= 5; i++ {
		grid[8][i] = get(uint(i))
	}
	grid[8][7] = get(6)
	grid[8][8] = get(7)
	grid[7][8] = get(8)
	for i := 9; i < 15; i++ {
		grid[14-i][8] = get(uint(i))
	}
	// 第二份(右上 + 左下)
	for i := 0; i < 7; i++ {
		grid[n-1-i][8] = get(uint(i))
	}
	for i := 7; i < 15; i++ {
		grid[8][n-15+i] = get(uint(i))
	}
}

// maskBit 返回掩码条件是否成立(成立处数据模块取反)。
func maskBit(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return (r*c)%2+(r*c)%3 == 0
	case 6:
		return ((r*c)%2+(r*c)%3)%2 == 0
	default:
		return ((r+c)%2+(r*c)%3)%2 == 0
	}
}

// penalty 掩码惩罚评分(N1 连续同色、N2 2×2 同色块、N3 类定位图形、N4 黑模块占比)。
func penalty(grid [][]bool) int {
	n := len(grid)
	score := 0
	// N1 + N3:逐行、逐列扫描
	line := make([]bool, n)
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if pass == 0 {
					line[j] = grid[i][j]
				} else {
					line[j] = grid[j][i]
				}
			}
			score += linePenalty(line)
		}
	}
	// N2:2×2 同色窗口
	for r := 0; r+1 < n; r++ {
		for c := 0; c+1 < n; c++ {
			if grid[r][c] == grid[r][c+1] && grid[r][c] == grid[r+1][c] && grid[r][c] == grid[r+1][c+1] {
				score += 3
			}
		}
	}
	// N4:黑模块占比偏离 50%
	dark := 0
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if grid[r][c] {
				dark++
			}
		}
	}
	percent := dark * 100 / (n * n)
	d := percent - 50
	if d < 0 {
		d = -d
	}
	score += d / 5 * 10
	return score
}

// 类定位图形模式(1:1:3:1:1 带两侧静区缩写为 1011101 + 0000)
var penaltyPat1 = []bool{true, false, true, true, true, false, true, false, false, false, false}
var penaltyPat2 = []bool{false, false, false, false, true, false, true, true, true, false, true}

func linePenalty(line []bool) int {
	score := 0
	// N1:≥5 连续同色
	runC, runLen := line[0], 1
	for _, v := range line[1:] {
		if v == runC {
			runLen++
		} else {
			if runLen >= 5 {
				score += 3 + runLen - 5
			}
			runC, runLen = v, 1
		}
	}
	if runLen >= 5 {
		score += 3 + runLen - 5
	}
	// N3:两种方向的 11 模块窗口匹配
	for i := 0; i+11 <= len(line); i++ {
		match1, match2 := true, true
		for k := 0; k < 11; k++ {
			if line[i+k] != penaltyPat1[k] {
				match1 = false
			}
			if line[i+k] != penaltyPat2[k] {
				match2 = false
			}
		}
		if match1 {
			score += 40
		}
		if match2 {
			score += 40
		}
	}
	return score
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}
