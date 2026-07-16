package analyzer

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"
	"tunnel-shm/internal/model"
	"tunnel-shm/internal/store"
	"tunnel-shm/internal/ws"
)

// Threshold 阈值配置
type Threshold struct {
	// 裂缝宽度变化速率阈值（mm/天）
	CrackRateWarning float64 `json:"crack_rate_warning"`
	CrackRateDanger  float64 `json:"crack_rate_danger"`
	// 位移变化速率阈值（mm/天）
	DisplacementRateWarning float64 `json:"displacement_rate_warning"`
	DisplacementRateDanger  float64 `json:"displacement_rate_danger"`
	// 应变变化速率阈值（με/天）
	StrainRateWarning float64 `json:"strain_rate_warning"`
	StrainRateDanger  float64 `json:"strain_rate_danger"`
}

// DefaultThreshold 默认阈值
var DefaultThreshold = Threshold{
	CrackRateWarning:        0.1,  // 裂缝宽度变化 0.1mm/天 告警
	CrackRateDanger:         0.3,  // 裂缝宽度变化 0.3mm/天 危险
	DisplacementRateWarning: 0.5,  // 位移变化 0.5mm/天 告警
	DisplacementRateDanger:  1.0,  // 位移变化 1.0mm/天 危险
	StrainRateWarning:       10.0, // 应变变化 10με/天 告警
	StrainRateDanger:        30.0, // 应变变化 30με/天 危险
}

// Analyzer 告警分析引擎
type Analyzer struct {
	store     *store.Store
	hub       *ws.Hub
	threshold Threshold
}

// New 创建分析器
func New(store *store.Store, hub *ws.Hub, threshold *Threshold) *Analyzer {
	t := DefaultThreshold
	if threshold != nil {
		t = *threshold
	}
	return &Analyzer{
		store:     store,
		hub:       hub,
		threshold: t,
	}
}

// AnalyzeAllSensors 分析所有传感器数据，检查是否触发告警
func (a *Analyzer) AnalyzeAllSensors(ctx context.Context) {
	log.Println("【分析-引擎】开始全量传感器分析...")
	startTime := time.Now()

	sections, err := a.store.GetSections(ctx)
	if err != nil {
		log.Printf("【分析-错误】获取断面列表失败: %v", err)
		return
	}

	alertCount := 0
	for _, section := range sections {
		sensors, err := a.store.GetSensorsBySection(ctx, section.ID)
		if err != nil {
			log.Printf("【分析-错误】获取断面[%s]传感器失败: %v", section.Code, err)
			continue
		}

		for _, sensor := range sensors {
			if a.analyzeSensor(ctx, &sensor, &section) {
				alertCount++
			}
		}
	}

	elapsed := time.Since(startTime)
	log.Printf("【分析-引擎】分析完成，断面数=%d, 触发告警数=%d, 耗时=%v",
		len(sections), alertCount, elapsed)
}

// analyzeSensor 分析单个传感器，返回是否触发告警
func (a *Analyzer) analyzeSensor(ctx context.Context, sensor *model.Sensor, section *model.Section) bool {
	rate, err := a.store.CalculateDeformationRate(ctx, sensor.ID)
	if err != nil {
		// 数据不足，跳过
		return false
	}

	// 取绝对值
	absRate := math.Abs(rate.Rate)

	var warningThreshold, dangerThreshold float64
	var metricName string

	switch sensor.Type {
	case model.SensorTypeCrack:
		warningThreshold = a.threshold.CrackRateWarning
		dangerThreshold = a.threshold.CrackRateDanger
		metricName = "裂缝宽度"
	case model.SensorTypeDisplacement:
		warningThreshold = a.threshold.DisplacementRateWarning
		dangerThreshold = a.threshold.DisplacementRateDanger
		metricName = "位移"
	case model.SensorTypeStrain:
		warningThreshold = a.threshold.StrainRateWarning
		dangerThreshold = a.threshold.StrainRateDanger
		metricName = "应变"
	}

	// 检查是否超过阈值
	var level model.AlertLevel
	var shouldAlert bool

	if absRate >= dangerThreshold {
		level = model.AlertLevelDanger
		shouldAlert = true
	} else if absRate >= warningThreshold {
		level = model.AlertLevelWarning
		shouldAlert = true
	}

	if !shouldAlert {
		return false
	}

	// 防重复告警（30分钟内相同告警不重复发送）
	hasRecent, err := a.store.CheckRecentAlert(ctx, sensor.ID, level, 30)
	if err != nil {
		log.Printf("【分析-错误】检查重复告警失败: %v", err)
	}
	if hasRecent {
		return false
	}

	// 确定变化方向
	direction := "增大"
	if rate.Rate < 0 {
		direction = "减小"
	}

	message := fmt.Sprintf(
		"【%s】断面[%s](%s)传感器[%s](%s) %s变化速率 %.3fmm/天，从 %.3fmm 到 %.3fmm，超过%s阈值 %.3fmm/天",
		level,
		section.Code, section.Name,
		sensor.Code, sensor.Position,
		metricName, absRate,
		rate.FirstValue, rate.LastValue,
		direction,
		warningThreshold,
	)

	alert := &model.Alert{
		SectionID:      section.ID,
		SensorID:       sensor.ID,
		Level:          level,
		Message:        message,
		DeformationRate: rate.Rate,
		Threshold:      warningThreshold,
		Status:         model.AlertStatusActive,
		TriggeredAt:    time.Now(),
	}

	if err := a.store.InsertAlert(ctx, alert); err != nil {
		log.Printf("【分析-错误】插入告警记录失败: %v", err)
		return false
	}

	log.Printf("【分析-告警】%s", message)

	// 通过WebSocket推送告警
	a.hub.BroadcastAlert(alert)

	return true
}

// AnalyzeSingleSensor 分析单个传感器（数据上报后实时检查）
func (a *Analyzer) AnalyzeSingleSensor(ctx context.Context, sensorID int) {
	sensor, section, err := a.store.GetSensorWithSection(ctx, sensorID)
	if err != nil {
		log.Printf("【分析-错误】获取传感器信息失败: %v", err)
		return
	}

	a.analyzeSensor(ctx, sensor, section)
}