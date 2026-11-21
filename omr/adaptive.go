package omr

import "sort"

// 逐题自适应判读阈值的可调参数(单位:墨迹占比 0~1)。
// 思路来自 OMRChecker 的行内跳变统计:同一行(题)的涂点里"已涂组"与"空白组"
// 的占比通常存在明显台阶,行内最大相邻跳变的中点比全局固定阈值更贴近该行的
// 真实墨迹水平 —— 对铅笔压力不均、复印件深浅不一、局部褪色鲁棒。
const (
	// adaptiveMinJump 行内最大相邻跳变达到该幅度才认为"涂/未涂"分界可信;
	// 不足则该行无清晰分界,回退全局阈值。
	adaptiveMinJump = 0.12
	// adaptiveClamp 逐题阈值允许低于全局阈值的最大幅度(防个别异常行跑飞)。
	adaptiveClamp = 0.15
	// adaptiveDeviation 记为"显著偏移"的阈值差:全卡过半行显著偏移时追加一条
	// 复印件/褪色告警(见 readMarks 的汇总统计)。
	adaptiveDeviation = 0.08
	// adaptiveFloor 逐题阈值的绝对下限:低于此占比的墨迹群不可信为"已涂"。
	adaptiveFloor = 0.15
)

// rowThreshold 依据一行涂点的墨迹占比估计该行的判读阈值:
//  1. 整行远低于全局阈值(空白行/纯纸面噪声)→ 直接用全局,防噪声被抬成"已涂";
//  2. 否则排序后找最大相邻跳变,跳幅 ≥ adaptiveMinJump 时取台阶中点为候选,
//     夹持进 [global-adaptiveClamp, global] 与绝对下限;跳变不足回退全局。
//
// 夹持只向下放宽、不向上收紧:轻涂漏判是固定阈值的实际痛点,而"抬高阈值抗噪"
// 的需求已由跳变幅度闸门把守;同时保证干净卡片上每行回退到与旧全局阈值完全
// 一致的判读语义(实心涂点≈1.0、空白≈0.0 的行内中点 ≈0.5,若允许上探会把
// 临界轻涂从"已涂"翻成"偏浅",属行为回归)。
//
// 不设"整行全涂"特判:整行饱满时全局阈值本就全部判已涂;而 Bradley 局部
// 二值化下空白泡的占比天然可达 0.25~0.3(环线渗入+畸变模糊),"行内最小值
// 接近全局阈值"不足以证明整行已涂,特判反而会把空白行整体误判成多涂。
func rowThreshold(ratios []float64, global float64) float64 {
	if len(ratios) < 2 {
		return global
	}
	sorted := append([]float64(nil), ratios...)
	sort.Float64s(sorted)
	if sorted[len(sorted)-1] < global-adaptiveClamp {
		return global
	}
	bestGap, bestIdx := 0.0, -1
	for i := 0; i+1 < len(sorted); i++ {
		if gap := sorted[i+1] - sorted[i]; gap > bestGap {
			bestGap, bestIdx = gap, i
		}
	}
	if bestGap < adaptiveMinJump {
		return global
	}
	cand := (sorted[bestIdx] + sorted[bestIdx+1]) / 2
	if cand > global {
		cand = global
	}
	if cand < global-adaptiveClamp {
		cand = global - adaptiveClamp
	}
	if cand < adaptiveFloor {
		cand = adaptiveFloor
	}
	return cand
}
