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
	// 按告警类型维度判重：rate 与 offline 是两个独立维度，不会互相抑制
	hasRecent, err := a.store.CheckRecentAlert(ctx, sensor.ID, level, 30, model.AlertTypeRate)
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

// DetectOfflineSensors 存活感知：检测所有未按预期上报数据的传感器
//
// 触发逻辑：
//   - 30 分钟无数据 → warning 告警（"数据缺失"）
//   - 120 分钟无数据 → danger 告警（升级为"设备离线"）
//   - 从未上报数据 → danger 告警（"设备未上线"）
//
// 防重复：
//   - 同传感器同级别离线告警 30 分钟内不重复触发
//   - 30 分钟无数据触发 warning 后又持续到 120 分钟，会自动升级为 danger
//     （因为 danger 的 30min 窗口会覆盖前一次的 warning）
//
// 设计要点：
//   - 单次 SQL 拉取所有传感器最近一次上报时间，避免 N 次 round-trip
//   - 不修改 sensors 表结构（"在线状态"是查询时实时计算的派生量，
//     与 deployment 状态解耦，避免采集器误更新 last_seen 字段）
//   - 一旦发现离线传感器，复用现有告警通道（alerts 表 + WebSocket 广播），
//     保证前端能在仪表盘上立刻看到
func (a *Analyzer) DetectOfflineSensors(ctx context.Context) {
	startTime := time.Now()

	pairs, err := a.store.GetSensorsWithSections(ctx)
	if err != nil {
		log.Printf("【分析-错误】获取传感器列表失败: %v", err)
		return
	}
	if len(pairs) == 0 {
		return
	}

	// 构造 sensorID 列表，单次 SQL 拉取所有最后上报时间
	ids := make([]int, 0, len(pairs))
	for _, p := range pairs {
		ids = append(ids, p.SensorID)
	}
	lastDataMap, err := a.store.GetSensorsLastDataAt(ctx, ids)
	if err != nil {
		log.Printf("【分析-错误】批量获取最近上报时间失败: %v", err)
		return
	}

	now := time.Now()
	offlineCount := 0
	staleCount := 0
	unknownCount := 0
	alertCount := 0

	for _, p := range pairs {
		lastData := lastDataMap[p.SensorID]
		state, mins := store.ComputeSensorState(lastData, now)

		switch state {
		case model.SensorStateOffline:
			offlineCount++
		case model.SensorStateStale:
			staleCount++
		case model.SensorStateUnknown:
			unknownCount++
		default:
			continue
		}

		// 离线与未上报的传感器需要进一步告警判定
		// 亚健康（stale）只统计不上报，避免误报
		if a.checkAndInsertOfflineAlert(ctx, p.SensorID, p.SectionID, state, mins) {
			alertCount++
		}
	}

	elapsed := time.Since(startTime)
	log.Printf("【分析-存活】扫描完成 传感器=%d 离线=%d 亚健康=%d 未上线=%d 触发告警=%d 耗时=%v",
		len(pairs), offlineCount, staleCount, unknownCount, alertCount, elapsed)
}

// checkAndInsertOfflineAlert 检查并插入离线告警（纯流程控制，无 SQL 外依赖）
// 返回是否成功触发了新告警（用于上层统计）
//
// thresholdMinutes 决定告警级别：
//   - mins == -1（从未上报） 或 mins >= SensorOfflineDangerThreshold (120min) -> danger
//   - 其余（30 <= mins < 120） -> warning
func (a *Analyzer) checkAndInsertOfflineAlert(
	ctx context.Context, sensorID, sectionID int,
	state model.SensorOnlineState, mins int,
) bool {
	var level model.AlertLevel
	switch {
	case state == model.SensorStateUnknown:
		// 从未上报视为最高级别（部署后一直没工作 = 最严重）
		level = model.AlertLevelDanger
	case mins >= int(store.SensorOfflineDangerThreshold.Minutes()):
		level = model.AlertLevelDanger
	default:
		level = model.AlertLevelWarning
	}

	// 防重复：同传感器同级别同类型告警 30 分钟内不重复
	hasRecent, err := a.store.CheckRecentAlert(ctx, sensorID, level, 30, model.AlertTypeOffline)
	if err != nil {
		log.Printf("【分析-错误】检查离线告警去重失败: %v", err)
	}
	if hasRecent {
		return false
	}

	// 拉取传感器与断面元信息，用于生成可读告警消息
	sensor, err := a.store.GetSensor(ctx, sensorID)
	if err != nil {
		log.Printf("【分析-错误】获取传感器[%d]信息失败: %v", sensorID, err)
		return false
	}
	section, err := a.store.GetSection(ctx, sectionID)
	if err != nil {
		log.Printf("【分析-错误】获取断面[%d]信息失败: %v", sectionID, err)
		return false
	}

	// 构造告警消息
	var message string
	switch state {
	case model.SensorStateUnknown:
		message = fmt.Sprintf(
			"【%s】断面[%s](%s)传感器[%s](%s) 自部署以来从未上报数据，疑似设备未上线或接线故障",
			level, section.Code, section.Name,
			sensor.Code, sensor.Position,
		)
	case model.SensorStateOffline:
		message = fmt.Sprintf(
			"【%s】断面[%s](%s)传感器[%s](%s) 已 %d 分钟无数据上报，超过预期周期 %d 分钟，疑似设备离线或数据缺失",
			level, section.Code, section.Name,
			sensor.Code, sensor.Position,
			mins, store.SensorExpectedIntervalMin,
		)
	default:
		// 不会走到这里（stale 状态已在外层过滤）
		return false
	}

	alert := &model.Alert{
		SectionID:   sectionID,
		SensorID:    sensorID,
		Level:       level,
		Type:        model.AlertTypeOffline,
		Message:     message,
		DeformationRate: 0,
		Threshold:   float64(store.SensorExpectedIntervalMin),
		Status:      model.AlertStatusActive,
		TriggeredAt: time.Now(),
	}

	if err := a.store.InsertAlert(ctx, alert); err != nil {
		log.Printf("【分析-错误】插入离线告警失败: %v", err)
		return false
	}

	// 告警插入成功后，异步触发该断面的健康度评分重算
	// （健康度的 completeness 维度会自动反映离线状态）
	if a.health != nil {
		a.health.EnqueueRecompute(sectionID)
	}

	log.Printf("【分析-告警】%s", message)

	// 通过 WebSocket 推送给前端仪表盘
	a.hub.BroadcastAlert(alert)
	return true
}

// GetSensorsLivenessBySection 拉取某断面下所有传感器的存活状态
// 用于前端在加载断面详情时一并展示"在线/离线"指示
func (a *Analyzer) GetSensorsLivenessBySection(ctx context.Context, sectionID int) ([]model.SensorLiveness, error) {
	sensors, err := a.store.GetSensorsBySection(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	if len(sensors) == 0 {
		return []model.SensorLiveness{}, nil
	}
	ids := make([]int, 0, len(sensors))
	for _, s := range sensors {
		ids = append(ids, s.ID)
	}
	lastDataMap, err := a.store.GetSensorsLastDataAt(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]model.SensorLiveness, 0, len(sensors))
	for _, s := range sensors {
		last := lastDataMap[s.ID]
		state, mins := store.ComputeSensorState(last, now)
		out = append(out, model.SensorLiveness{
			SensorID:             s.ID,
			SectionID:            s.SectionID,
			LastDataAt:           last,
			State:                state,
			MinutesSinceLastData: mins,
			ExpectedIntervalMin:  store.SensorExpectedIntervalMin,
		})
	}
	return out, nil
}

// SetHealthScheduler 注入健康度调度器，告警插入后异步触发对应断面评分重算
func (a *Analyzer) SetHealthScheduler(s *healthscore.Scheduler) {
	a.health = s
}

// AutoResolveRecoveredAlerts 自动关闭已恢复的告警
//
// 修复"告警数据已恢复但 active 状态长期留存"问题。
// 定时任务每 5 分钟扫描所有 active 告警，对每条告警判定对应传感器的实时状态：
//   - rate 类型    ：当前 24h 多窗口最严速率 |rate| 低于 warning 阈值 → 数据已恢复
//   - offline 类型：最近一次上报在 10 分钟内（online 状态）→ 设备已恢复
//   - 缺省 type    ：兼容历史数据，按 rate 规则判定
//
// 满足恢复条件的告警会批量更新为 resolved 状态。resolved 完成后通过
// WebSocket 广播 "alert_resolved" 消息，前端可即时从活跃列表移除并刷新概览。
//
// 返回值：扫描告警数、关闭告警数、错误
func (a *Analyzer) AutoResolveRecoveredAlerts(ctx context.Context) (int, int, error) {
	startTime := time.Now()
	activeAlerts, err := a.store.GetActiveAlerts(ctx)
	if err != nil {
		log.Printf("【分析-恢复-错误】查询活跃告警失败: %v", err)
		return 0, 0, err
	}
	if len(activeAlerts) == 0 {
		return 0, 0, nil
	}

	// 按 sensorID 缓存每台传感器的实时状态，避免同一传感器在多条告警上重复查询
	type sensorState struct {
		sensor   *model.Sensor
		rate     *model.DeformationRate
		rateErr  error
		lastData *time.Time
		lastErr  error
	}
	cache := make(map[int]*sensorState, len(activeAlerts))

	toResolve := make([]int, 0, len(activeAlerts))
	for _, alert := range activeAlerts {
		ss, ok := cache[alert.SensorID]
		if !ok {
			sensor, err := a.store.GetSensor(ctx, alert.SensorID)
			if err != nil {
				log.Printf("【分析-恢复-错误】获取传感器[%d]信息失败: %v", alert.SensorID, err)
				continue
			}
			ss = &sensorState{sensor: sensor}
			cache[alert.SensorID] = ss
		}

		recovered := false
		// 历史告警可能没有 type 字段，按 rate 规则兼容处理
		alertType := alert.Type
		if alertType == "" {
			alertType = model.AlertTypeRate
		}

		switch alertType {
		case model.AlertTypeRate:
			// rate 类告警：实时计算 24h 速率
			if ss.rate == nil && ss.rateErr == nil {
				rate, rerr := a.store.CalculateDeformationRate(ctx, ss.sensor.ID)
				ss.rate, ss.rateErr = rate, rerr
			}
			if ss.rateErr != nil {
				// 数据不足（采集器刚部署 / 数据未恢复） → 暂不判定为恢复
				log.Printf("【分析-恢复-跳过】传感器[%d] 24h数据不足: %v", ss.sensor.ID, ss.rateErr)
				continue
			}
			if !exceedsWarningThreshold(ss.sensor.Type, math.Abs(ss.rate.Rate), a.threshold) {
				recovered = true
			}
		case model.AlertTypeOffline:
			// offline 类告警：检查最近一次上报时间
			if ss.lastData == nil && ss.lastErr == nil {
				ts, lerr := a.store.GetSensorLastDataAt(ctx, ss.sensor.ID)
				ss.lastData, ss.lastErr = ts, lerr
			}
			if ss.lastErr != nil {
				log.Printf("【分析-恢复-错误】获取传感器[%d]最近上报时间失败: %v", ss.sensor.ID, ss.lastErr)
				continue
			}
			// 仅当最近一次上报 < 10 分钟（online 状态）才判定恢复
			if ss.lastData != nil {
				_, mins := store.ComputeSensorState(ss.lastData, time.Now())
				if mins >= 0 && mins < int(store.SensorOnlineThreshold.Minutes()) {
					recovered = true
				}
			}
		}

		if recovered {
			toResolve = append(toResolve, alert.ID)
		}
	}

	if len(toResolve) == 0 {
		log.Printf("【分析-恢复】扫描active告警=%d，已恢复=0 耗时=%v", len(activeAlerts), time.Since(startTime))
		return len(activeAlerts), 0, nil
	}

	closed, err := a.store.AutoResolveAlerts(ctx, toResolve)
	if err != nil {
		log.Printf("【分析-恢复-错误】批量关闭告警失败: %v", err)
		return len(activeAlerts), 0, err
	}

	log.Printf("【分析-恢复】扫描active告警=%d，待关闭=%d，已成功关闭=%d 耗时=%v",
		len(activeAlerts), len(toResolve), closed, time.Since(startTime))

	// 通过 WebSocket 广播告警已自动恢复，前端可即时从活跃列表移除
	if a.hub != nil && closed > 0 {
		a.hub.BroadcastAlertsResolved(toResolve, closed)
	}

	return len(activeAlerts), closed, nil
}

// exceedsWarningThreshold 判断绝对速率是否达到指定传感器类型的告警 warning 阈值
// 抽出为纯函数便于单元测试覆盖各传感器类型
//
// 设计要点：
//   - 与 analyzeSensor 内的告警判定逻辑保持完全一致：
//     absRate >= warningThreshold 即认为需要告警
//   - 未知传感器类型返回 false（保守地不恢复）
func exceedsWarningThreshold(sensorType model.SensorType, absRate float64, t Threshold) bool {
	var warningThreshold float64
	switch sensorType {
	case model.SensorTypeCrack:
		warningThreshold = t.CrackRateWarning
	case model.SensorTypeDisplacement:
		warningThreshold = t.DisplacementRateWarning
	case model.SensorTypeStrain:
		warningThreshold = t.StrainRateWarning
	default:
		return false
	}
	return absRate >= warningThreshold
}