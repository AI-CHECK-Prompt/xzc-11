// Package healthscore 断面健康度评分引擎（纯函数实现，与 DB/IO 解耦）
package healthscore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"tunnel-shm/internal/model"
)

// SensorScoreInput 单个传感器的评分输入
type SensorScoreInput struct {
	SensorID           int
	SensorType         model.SensorType
	Rate24h            float64
	Rate7d             float64
	Rate30d            float64
	RecentAlertCount   int
	DataCompleteness   float64 // 0~1，1=完整
	HistoricalVariance float64 // 30d 窗口内的标准差（与单位一致）
}

// ComputeSectionScore 纯函数：计算单个断面的健康度评分。
// 返回：score（总分）、details（每维度明细）、intermediate（每传感器中间数据）
//
// 评分构成：
//   - 每个传感器子分 = 100 - 当前告警扣分 - 趋势扣分 - 稳定性扣分 + 完整度奖励
//   - 断面分 = Σ(传感器子分 × 传感器类型权重) × 位置敏感度
//   - 断面分钳制到 [0, 100]
func ComputeSectionScore(
	_ context.Context,
	section model.Section,
	sensors []model.Sensor,
	inputs []SensorScoreInput,
) (*model.SectionHealthScore, []model.ScoreDetail, []model.ScoreIntermediate, error) {

	if len(sensors) == 0 || len(sensors) != len(inputs) {
		return nil, nil, nil, fmt.Errorf("传感器与输入数据数量不匹配")
	}

	posSens := model.PositionSensitivity[section.PositionType]
	if posSens == 0 {
		posSens = 1.0
	}

	// 阈值映射（与 analyzer.DefaultThreshold 保持一致；缺省走 mm/天）
	warning := map[model.SensorType]float64{
		model.SensorTypeDisplacement: 0.5,
		model.SensorTypeCrack:        0.1,
		model.SensorTypeStrain:       10.0,
	}
	danger := map[model.SensorType]float64{
		model.SensorTypeDisplacement: 1.0,
		model.SensorTypeCrack:        0.3,
		model.SensorTypeStrain:       30.0,
	}

	var details []model.ScoreDetail
	var intermediates []model.ScoreIntermediate
	sensorScores := make(map[model.SensorType]float64, 3)

	// 触发类型由调用方注入（默认 cron；scheduler 会在事件触发时再调一次并设 event）
	trigger := model.HealthTriggerCron
	now := time.Now()

	for i, sensor := range sensors {
		_ = sensor // sensors 与 inputs 严格 1:1，索引对齐已在调用方保证
		in := inputs[i]
		wThr := warning[in.SensorType]
		dThr := danger[in.SensorType]

		// === 维度 1：当前告警（占传感器子分 40%）===
		// 24h 速率与阈值对比
		abs24 := math.Abs(in.Rate24h)
		alertSub := 100.0
		var alertRaw float64 = abs24
		if abs24 >= dThr {
			alertSub = 0
		} else if abs24 >= wThr {
			alertSub = 100 * (1 - (abs24-wThr)/(dThr-wThr))
		} else {
			// 低于 warning：按比例轻微扣分，速率/警告阈值 * 30 上限
			alertSub = 100 - math.Min(30, abs24/wThr*30)
		}
		// 近期告警次数额外扣分（每次 -5，最低 0）
		alertSub -= math.Min(40, float64(in.RecentAlertCount)*5)
		if alertSub < 0 {
			alertSub = 0
		}

		// === 维度 2：近期变化趋势（占 30%）===
		// 用 7d 速率与 30d 速率对比，加速恶化扣分
		trendSub := 100.0
		var trendRaw float64 = in.Rate7d
		acc := math.Abs(in.Rate7d) - math.Abs(in.Rate30d)
		if acc > 0 {
			// 加速恶化：每加速 wThr 的 1 倍扣 25 分
			trendSub = 100 - math.Min(100, acc/wThr*25)
		} else {
			// 减速或稳定：轻微奖励
			trendSub = 100 + math.Min(10, -acc/wThr*5)
		}
		if trendSub > 100 {
			trendSub = 100
		}
		if trendSub < 0 {
			trendSub = 0
		}

		// === 维度 3：历史稳定性（占 20%）===
		// 30d 方差越大越不稳定，按 wThr 归一化
		stabilitySub := 100.0
		var stabilityRaw float64 = in.HistoricalVariance
		if in.HistoricalVariance > 0 {
			stabilitySub = 100 - math.Min(100, in.HistoricalVariance/wThr*50)
		}
		if stabilitySub < 0 {
			stabilitySub = 0
		}

		// === 维度 4：数据完整性（占 10%）===
		// 完整度 1.0 时为 100，0 时为 0
		completenessSub := math.Max(0, math.Min(100, in.DataCompleteness*100))

		// === 加权聚合到传感器子分 ===
		sensorSub := alertSub*model.DimensionWeights.Alert +
			trendSub*model.DimensionWeights.Trend +
			stabilitySub*model.DimensionWeights.Stability +
			completenessSub*model.DimensionWeights.Completeness
		if sensorSub < 0 {
			sensorSub = 0
		}
		if sensorSub > 100 {
			sensorSub = 100
		}
		sensorScores[in.SensorType] = sensorSub

		// 写入明细（4 维度 × 1 传感器 = 4 行）
		dim := func(name, sub string, raw, subScore, w float64, expl string) model.ScoreDetail {
			return model.ScoreDetail{
				Dimension:    name,
				SubDimension: sub,
				RawValue:     raw,
				SubScore:     subScore,
				Weight:       w,
				Contribution: subScore * w,
				Explanation:  expl,
				CalculatedAt: now,
			}
		}
		details = append(details,
			dim("alert", string(in.SensorType), alertRaw, alertSub, model.DimensionWeights.Alert,
				fmt.Sprintf("24h速率=%.3f, 警告阈值=%.3f, 危险阈值=%.3f, 近期告警=%d次",
					alertRaw, wThr, dThr, in.RecentAlertCount)),
			dim("trend", string(in.SensorType), trendRaw, trendSub, model.DimensionWeights.Trend,
				fmt.Sprintf("7d速率=%.3f, 30d速率=%.3f, 加速量=%.3f", in.Rate7d, in.Rate30d, acc)),
			dim("stability", string(in.SensorType), stabilityRaw, stabilitySub, model.DimensionWeights.Stability,
				fmt.Sprintf("30d标准差=%.3f", stabilityRaw)),
			dim("completeness", string(in.SensorType), in.DataCompleteness, completenessSub, model.DimensionWeights.Completeness,
				fmt.Sprintf("7d数据完整度=%.2f%%", completenessSub)),
		)

		// 中间数据
		inputsJSON, _ := json.Marshal(in)
		intermediates = append(intermediates, model.ScoreIntermediate{
			SensorID:           in.SensorID,
			SectionID:          section.ID,
			SensorType:         in.SensorType,
			Rate24h:            in.Rate24h,
			Rate7d:             in.Rate7d,
			Rate30d:            in.Rate30d,
			RecentAlertCount:   in.RecentAlertCount,
			DataCompleteness:   in.DataCompleteness,
			HistoricalVariance: in.HistoricalVariance,
			SensorSubScore:     sensorSub,
			InputsJSON:         string(inputsJSON),
			CalculatedAt:       now,
		})
	}

	// === 按传感器类型加权聚合到断面 ===
	var totalWeight, weightedSum float64
	for _, st := range []model.SensorType{model.SensorTypeDisplacement, model.SensorTypeCrack, model.SensorTypeStrain} {
		w := model.SensorTypeWeight[st]
		totalWeight += w
		if sub, ok := sensorScores[st]; ok {
			weightedSum += sub * w
		}
	}
	if totalWeight == 0 {
		return nil, nil, nil, fmt.Errorf("无有效传感器类型")
	}
	sectionScore := weightedSum / totalWeight

	// 位置敏感度：>1 放大扣分（乘以 1/sensitivity 后钳制，等价于"分扣更多"）
	// 实现：score = 100 - (100 - sectionScore) * sensitivity
	sectionScore = 100 - (100-sectionScore)*posSens
	if sectionScore < 0 {
		sectionScore = 0
	}
	if sectionScore > 100 {
		sectionScore = 100
	}

	// 计算各传感器子分（位移/裂缝/应变）作为断面级别的展示字段
	disp := sensorScores[model.SensorTypeDisplacement]
	crk := sensorScores[model.SensorTypeCrack]
	str := sensorScores[model.SensorTypeStrain]

	// 维度总分（按传感器类型加权后的）
	var alertDim, trendDim, stabilityDim, completenessDim float64
	var wTotal float64
	for _, d := range details {
		switch d.Dimension {
		case "alert":
			alertDim += d.Contribution
		case "trend":
			trendDim += d.Contribution
		case "stability":
			stabilityDim += d.Contribution
		case "completeness":
			completenessDim += d.Contribution
		}
		wTotal += d.Weight
	}
	if wTotal > 0 {
		alertDim /= wTotal
		trendDim /= wTotal
		stabilityDim /= wTotal
		completenessDim /= wTotal
	}

	grade := model.GradeFromScore(sectionScore)
	score := &model.SectionHealthScore{
		SectionID:                  section.ID,
		TotalScore:                 sectionScore,
		Grade:                      grade,
		DisplacementScore:          disp,
		CrackScore:                 crk,
		StrainScore:                str,
		AlertDimensionScore:        alertDim,
		TrendDimensionScore:        trendDim,
		StabilityDimensionScore:    stabilityDim,
		CompletenessDimensionScore: completenessDim,
		PositionType:               section.PositionType,
		Sensitivity:                posSens,
		TriggerType:                trigger,
		CalculatedAt:               now,
	}

	// 给 details 补充 sectionID（已知）
	for i := range details {
		details[i].SectionID = section.ID
	}
	return score, details, intermediates, nil
}
