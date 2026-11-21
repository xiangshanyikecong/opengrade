// Package qrcode 实现 QR 码(数字模式)的编码与解码,覆盖版本 v1~v4、纠错级 M。
// 用于答题卡考号二维码通道:card 印染模块矩阵,omr 在透视矫正后的标准网格上
// 按已知矩形定位解码(不做全局检索)。纯 Go、零第三方依赖。
//
// 本文件:GF(256) 有限域运算与 Reed-Solomon 编解码(本原多项式 0x11D,生成元 α=2,
// 与 QR 规范一致;纠错码字生成多项式 Π(x-α^i), i=0..n-1)。
package qrcode

import "fmt"

// GF(256) 指数/对数表(本原多项式 0x11D)。
var (
	gfExp [512]byte
	gfLog [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func gfDiv(a, b byte) byte {
	if b == 0 {
		panic("qrcode: GF(256) 除零")
	}
	if a == 0 {
		return 0
	}
	return gfExp[(int(gfLog[a])+255-int(gfLog[b]))%255]
}

// gfPow 幂运算支持负指数(负指数按乘法逆元处理)。
func gfPow(a byte, n int) byte {
	if a == 0 {
		if n < 0 {
			panic("qrcode: 0 的负幂无定义")
		}
		return 0
	}
	if n < 0 {
		n = (n%255 + 255) % 255
	}
	return gfExp[(int(gfLog[a])*n)%255]
}

func gfInv(a byte) byte { return gfPow(a, -1) }

// polyMulDesc 降幂多项式乘(p[0] 为最高次)。
func polyMulDesc(p, q []byte) []byte {
	out := make([]byte, len(p)+len(q)-1)
	for i, a := range p {
		for j, b := range q {
			out[i+j] ^= gfMul(a, b)
		}
	}
	return out
}

// polyEvalDesc Horner 求值:p(a),p[0] 为最高次系数。
func polyEvalDesc(p []byte, a byte) byte {
	y := p[0]
	for _, c := range p[1:] {
		y = gfMul(y, a) ^ c
	}
	return y
}

// rsGenerator 生成 n 个纠错码字的生成多项式:Π(x-α^i), i=0..n-1,降幂系数。
func rsGenerator(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		g = polyMulDesc(g, []byte{1, gfPow(2, i)})
	}
	return g
}

// rsEncode 计算数据块的纠错码字(多项式除法取余)。
func rsEncode(data []byte, ecLen int) []byte {
	gen := rsGenerator(ecLen)
	rem := make([]byte, len(data)+ecLen)
	copy(rem, data)
	for i := 0; i < len(data); i++ {
		factor := rem[i]
		if factor == 0 {
			continue
		}
		for j, g := range gen {
			rem[i+j] ^= gfMul(g, factor)
		}
	}
	return rem[len(data):]
}

// rsDecode 纠正接收码字 msg(data+ecLen 个,msg[0] 为最高次)中的错误,
// 返回纠错后的数据部分;错误超过 ecLen/2 处或定位失败时返回错误。
// 路径:伴随式 → Berlekamp-Massey 错误定位多项式(降幂) → Chien 搜索 → Forney 求值 → 伴随式复核。
func rsDecode(msg []byte, ecLen int) ([]byte, error) {
	n := len(msg)
	synd := make([]byte, ecLen) // S_i = C(α^i),i=0..ecLen-1
	for i := range synd {
		s := byte(0)
		a := gfPow(2, i)
		for _, c := range msg {
			s = gfMul(s, a) ^ c
		}
		synd[i] = s
	}
	clean := true
	for _, s := range synd {
		if s != 0 {
			clean = false
			break
		}
	}
	if clean {
		return msg[:n-ecLen], nil
	}

	// Berlekamp-Massey(降幂系数列表,errLoc[0] 恒 1)
	errLoc := []byte{1}
	oldLoc := []byte{1}
	for i := 0; i < ecLen; i++ {
		delta := synd[i]
		for j := 1; j < len(errLoc); j++ {
			delta ^= gfMul(errLoc[len(errLoc)-1-j], synd[i-j])
		}
		oldLoc = append(oldLoc, 0)
		if delta != 0 {
			if len(oldLoc) > len(errLoc) {
				newLoc := polyScaleDesc(oldLoc, delta)
				oldLoc = polyScaleDesc(errLoc, gfInv(delta))
				errLoc = newLoc
			}
			errLoc = polyAddDesc(errLoc, polyScaleDesc(oldLoc, delta))
		}
	}
	errCount := len(errLoc) - 1
	if errCount > ecLen/2 {
		return nil, fmt.Errorf("错误数 %d 超过纠错能力 %d", errCount, ecLen/2)
	}

	// Chien 搜索:错误在数组下标 p 处 ⟺ 定位子 X=α^(n-1-p) ⟺ Λ 的根 X^-1=α^(p-n+1)。
	// 逐位置代入求值,根出现即定位(降幂 errLoc 的常数项恒为 1)。
	errPos := []int{}
	for p := 0; p < n; p++ {
		if polyEvalDesc(errLoc, gfPow(2, p-(n-1))) == 0 {
			errPos = append(errPos, p)
		}
	}
	if len(errPos) != errCount || errCount == 0 {
		return nil, fmt.Errorf("Chien 定位失败:期望 %d 处错误,找到 %d 处", errCount, len(errPos))
	}

	// Forney(按本包约定直接推导):
	//   Ω(x) = S(x)·Λ(x) mod x^ecLen(S、Λ 均升幂),deg Ω < 错误数;
	//   Ω(X_m^-1) = e_m·Π_{j≠m}(1 - X_j·X_m^-1) ⟹ e_m = Ω(X_m^-1) / Π(1 - X_j·X_m^-1)。
	// X_k = α^coefPos_k(coefPos = n-1-p,错误值在码字多项式中的次数)。
	coefPos := make([]int, len(errPos))
	lambda := []byte{1} // 升幂 Λ(x) = Π(1 + X_j·x)
	for i, p := range errPos {
		coefPos[i] = n - 1 - p
		next := make([]byte, len(lambda)+1)
		X := gfPow(2, coefPos[i])
		for j, c := range lambda {
			next[j] ^= c
			next[j+1] ^= gfMul(c, X)
		}
		lambda = next
	}
	omega := make([]byte, ecLen) // Ω = S·Λ mod x^ecLen(升幂截断)
	for i, s := range synd {
		if s == 0 {
			continue
		}
		for j, l := range lambda {
			if i+j >= ecLen {
				break
			}
			omega[i+j] ^= gfMul(s, l)
		}
	}
	for i, p := range errPos {
		X := gfPow(2, coefPos[i])
		xinv := gfInv(X)
		num := byte(0) // Ω(X^-1)
		xp := byte(1)
		for _, c := range omega {
			num ^= gfMul(c, xp)
			xp = gfMul(xp, xinv)
		}
		den := byte(1)
		for j := range coefPos {
			if j == i {
				continue
			}
			den = gfMul(den, 1^gfMul(xinv, gfPow(2, coefPos[j])))
		}
		msg[p] ^= gfDiv(num, den)
	}
	for i := 0; i < ecLen; i++ {
		s := byte(0)
		a := gfPow(2, i)
		for _, c := range msg {
			s = gfMul(s, a) ^ c
		}
		if s != 0 {
			return nil, fmt.Errorf("纠错后伴随式复核失败")
		}
	}
	return msg[:n-ecLen], nil
}

// polyScaleDesc 降幂多项式数乘。
func polyScaleDesc(p []byte, x byte) []byte {
	out := make([]byte, len(p))
	for i, c := range p {
		out[i] = gfMul(c, x)
	}
	return out
}

// polyAddDesc 降幂多项式相加(低位对齐,高位补零)。
func polyAddDesc(p, q []byte) []byte {
	out := make([]byte, max(len(p), len(q)))
	for i := range out {
		var a, b byte
		if i < len(p) {
			a = p[len(p)-1-i]
		}
		if i < len(q) {
			b = q[len(q)-1-i]
		}
		out[len(out)-1-i] = a ^ b
	}
	return out
}
