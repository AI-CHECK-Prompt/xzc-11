// 趋势维度变化方向判断专项验证
//
// 复现并量化"恢复性变化场景下趋势维度扣分"的争议：
//   - 输入：7d 速率 0.05 / 30d 速率 0.12（加速量 -0.07，已开始减速）
//   - 预期：趋势维度识别为"减速/恢复"，至少不应扣分
//   - 现状追踪：通过 score.details[1]（dimension=trend）逐项打印对比
//
// 此外覆盖 5 类典型方向组合：
//   1) 加速恶化  7d=0.20 > 30d=0.05  acc=+0.15
//   2) 减速恢复  7d=0.05 < 30d=0.12  acc=-0.07
//   3) 平稳维持  7d=0.05 == 30d=0.05 acc=0
//   4) 反向回落  7d=-0.05 (值开始回落) 30d=+0.12  acc=abs 比较失真
//   5) 显著恢复  7d=0.02 30d=0.30    acc=-0.28  大幅减速
package healthscore

import (
	"context"
	"fmt"
	"math"
	"testing"

	"tunnel-shm/internal/model"
)

// runTrendCase 运行单条 case 并返回 trend 维度明细
func runTrendCase(t *testing.T, label string, rate7, rate30 float64) {
	t.Helper()
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeCrack)}
	inputs := []SensorScoreInput{
		{
			SensorID: 11, SensorType: model.SensorTypeCrack,
			Rate24h: rate7, Rate7d: rate7, Rate30d: rate30,
			RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.01,
		},
	}
	score, details, _, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err != nil {
		t.Fatalf("[%s] 计算失败: %v", label, err)
	}
	var trendSub, alertSub, stabilitySub, completenessSub, raw float64
	for _, d := range details {
		switch d.Dimension {
		case "trend":
			trendSub = d.SubScore
			raw = d.RawValue
		case "alert":
			alertSub = d.SubScore
		case "stability":
			stabilitySub = d.SubScore
		case "completeness":
			completenessSub = d.SubScore
		}
	}
	acc := math.Abs(rate7) - math.Abs(rate30)
	direction := "加速恶化(acc>0)"
	if acc < 0 {
		direction = "减速恢复(acc<0)"
	} else if acc == 0 {
		direction = "平稳维持(acc=0)"
	}
	t.Logf("=== %s | 方向=%s ===", label, direction)
	t.Logf("  7d=%.3f  30d=%.3f  acc=%+.3f", rate7, rate30, acc)
	t.Logf("  trend维度    sub=%.2f  raw=%.3f", trendSub, raw)
	t.Logf("  alert维度    sub=%.2f", alertSub)
	t.Logf("  stability维度 sub=%.2f", stabilitySub)
	t.Logf("  completeness sub=%.2f", completenessSub)
	t.Logf("  传感器子分=%.2f  断面分=%.2f  等级=%s",
		score.CrackScore, score.TotalScore, score.Grade)
}

// TestTrendDimension_RecoveryDirection 复现运维反馈的"恢复反而扣分"场景
func TestTrendDimension_RecoveryDirection(t *testing.T) {
	runTrendCase(t, "Case1-加速恶化", 0.20, 0.05)
	runTrendCase(t, "Case2-减速恢复（用户反馈场景）", 0.05, 0.12)
	runTrendCase(t, "Case3-平稳维持", 0.05, 0.05)
	runTrendCase(t, "Case4-反向回落", -0.05, 0.12)
	runTrendCase(t, "Case5-显著恢复", 0.02, 0.30)
}

// TestTrendDimension_AccelerationMagnitude 量化"加速扣分幅度"与"减速奖励幅度"
//   揭示非对称性：加速时按 wThr 比例扣 25，减速时按 wThr 比例奖励 5（封顶 10）
func TestTrendDimension_AccelerationMagnitude(t *testing.T) {
	wThr := 0.1 // crack 警告阈值
	cases := []struct {
		rate7, rate30 float64
	}{
		{0.06, 0.05},  // 几乎不加速
		{0.10, 0.05},  // 加速量 = wThr 的 50%
		{0.15, 0.05},  // 加速量 = wThr 的 100%
		{0.25, 0.05},  // 加速量 = wThr 的 200%（已封顶）
		{0.05, 0.10},  // 减速量 = wThr 的 50%
		{0.05, 0.20},  // 减速量 = wThr 的 150%
		{0.05, 0.50},  // 减速量 = wThr 的 450%（已封顶）
	}
	for _, c := range cases {
		acc := math.Abs(c.rate7) - math.Abs(c.rate30)
		trendSub := 100.0
		if acc > 0 {
			trendSub = 100 - math.Min(100, acc/wThr*25)
		} else {
			trendSub = 100 + math.Min(10, -acc/wThr*5)
		}
		if trendSub > 100 {
			trendSub = 100
		}
		if trendSub < 0 {
			trendSub = 0
		}
		t.Logf("7d=%.3f 30d=%.3f acc=%+.3f  →  trendSub=%.2f", c.rate7, c.rate30, acc, trendSub)
	}
}

// TestTrendDimension_AbsLosesSign 演示 math.Abs 在跨零场景丢失方向信息
// 场景：30d 期间整体上升（rate30=+0.12），近 7d 数值开始回落（rate7=-0.05）
// 当前实现：acc = abs(-0.05) - abs(0.12) = -0.07 → 走"减速奖励"分支
// 但实际上方向完全反转，应该给予比"小幅减速"更大的奖励
func TestTrendDimension_AbsLosesSign(t *testing.T) {
	cases := []struct {
		rate7, rate30 float64
	}{
		{0.05, 0.12},  // 减速但仍同向：轻微奖励 +3.5
		{-0.05, 0.12}, // 反向回落：当前 abs 算法也只奖励 +3.5（应更多）
		{-0.10, 0.12}, // 显著反向：abs 算法也只奖励 +3.5（应更多）
		{-0.30, 0.12}, // 强力反向：abs 算法也只奖励 +3.5（应更多）
	}
	wThr := 0.1
	for _, c := range cases {
		acc := math.Abs(c.rate7) - math.Abs(c.rate30)
		trendSub := 100.0
		if acc > 0 {
			trendSub = 100 - math.Min(100, acc/wThr*25)
		} else {
			trendSub = 100 + math.Min(10, -acc/wThr*5)
		}
		if trendSub > 100 {
			trendSub = 100
		}
		// 真实方向判断：rate7 < 0 表示数值已开始回落
		trueDir := "同向"
		if c.rate7*c.rate30 < 0 {
			trueDir = "方向反转(数值回落)"
		}
		fmt.Printf("7d=%+.3f 30d=%+.3f acc=%+.3f trendSub=%.2f | 真实方向=%s\n",
			c.rate7, c.rate30, acc, trendSub, trueDir)
	}
}
