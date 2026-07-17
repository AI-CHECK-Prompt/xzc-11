package analyzer

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"
	"tunnel-shm/internal/healthscore"
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
	health    *healthscore.Scheduler
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

	// rate.Rate 已是归一化到 mm/天 的最严速率（多窗口分析绝对值最大者）
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

	// 描述触发来源：明确指出告警是源于端点差值、滑动窗口还是相邻阶跃
	// 避免"首末两点相消"导致的漏报（如先抬升 12.3→14.8mm 再回落 12.5mm）
	var sourceDesc string
	var detailDesc string
	switch rate.RateSource {
	case model.RateSourceSlidingWin:
		sourceDesc = fmt.Sprintf("1h滑动窗口内最大变化量 %.3f→%.3f",
			rate.SlidingStartVal, rate.SlidingEndVal)
		detailDesc = fmt.Sprintf("，窗口跨度=%.3fmm", math.Abs(rate.SlidingEndVal-rate.SlidingStartVal))
	case model.RateSourceStep:
		sourceDesc = fmt.Sprintf("相邻数据点阶跃 %.3f→%.3f",
			rate.StepFromVal, rate.StepToVal)
		detailDesc = fmt.Sprintf("，阶跃量=%.3fmm", math.Abs(rate.StepToVal-rate.StepFromVal))
	default:
		sourceDesc = fmt.Sprintf("24h端点差值 %.3f→%.3f",
			rate.FirstValue, rate.LastValue)
		detailDesc = ""
	}

	message := fmt.Sprintf(
		"【%s】断面[%s](%s)传感器[%s](%s) %s变化速率 %.3f%s/天，触发来源=%s%s；窗口极值 %.3f~%.3f%s；超过%s阈值 %.3f",
		level,
		section.Code, section.Name,
		sensor.Code, sensor.Position,
		metricName, absRate, unitSuffix(sensor.Type),
		sourceDesc, detailDesc,
		rate.MinValue, rate.MaxValue, unitSuffix(sensor.Type),
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

	// 告警插入成功后，异步触发该断面的健康度评分重算
	// （节流由 scheduler 内部保证：同一断面 30s 内不重复重算）
	if a.health != nil {
		a.health.EnqueueRecompute(section.ID)
	}

	log.Printf("【分析-告警】%s", message)

	// 通过WebSocket推送告警
	a.hub.BroadcastAlert(alert)

	return true
}

// unitSuffix 返回传感器类型对应的单位后缀（用于告警消息展示）
func unitSuffix(t model.SensorType) string {
	switch t {
	case model.SensorTypeDisplacement, model.SensorTypeCrack:
		return "mm"
	case model.SensorTypeStrain:
		return "με"
	default:
		return ""
	}
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

// AnalyzeSectionByID 分析指定断面下的所有传感器（验收/调试用）
//
// 与 AnalyzeAllSensors 的区别：本方法只分析一个断面，速度快，
// 用于验收脚本在数据上报后立刻检测告警，避免等待 5 分钟 cron。
func (a *Analyzer) AnalyzeSectionByID(ctx context.Context, sectionID int) {
	sec, err := a.store.GetSection(ctx, sectionID)
	if err != nil {
		log.Printf("【分析-错误】获取断面[%d]失败: %v", sectionID, err)
		return
	}
	sensors, err := a.store.GetSensorsBySection(ctx, sectionID)
	if err != nil {
		log.Printf("【分析-错误】获取断面[%d]传感器失败: %v", sectionID, err)
		return
	}
	cnt := 0
	for i := range sensors {
		if a.analyzeSensor(ctx, &sensors[i], sec) {
			cnt++
		}
	}
	log.Printf("【分析-断面】断面[%s] 分析完成，触发告警=%d", sec.Code, cnt)
}

// SetHealthScheduler 注入健康度调度器，告警插入后异步触发对应断面评分重算
func (a *Analyzer) SetHealthScheduler(s *healthscore.Scheduler) {
	a.health = s
}