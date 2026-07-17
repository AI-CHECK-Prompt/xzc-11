package api

import (
	"context"
	"net/http"
	"strconv"
	"time"
	"tunnel-shm/internal/analyzer"
	"tunnel-shm/internal/model"
	"tunnel-shm/internal/store"

	"github.com/gin-gonic/gin"
)

// Handler API处理器
type Handler struct {
	store    *store.Store
	engine   *gin.Engine
	analyzer *analyzer.Analyzer
}

// NewHandler 创建API处理器
func NewHandler(store *store.Store, engine *gin.Engine, anal *analyzer.Analyzer) *Handler {
	return &Handler{store: store, engine: engine, analyzer: anal}
}

// RegisterRoutes 注册路由
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	h.engine = r
	api := r.Group("/api/v1")
	{
		// 断面相关
		api.GET("/sections", h.GetSections)
		api.GET("/sections/:id", h.GetSection)
		api.GET("/sections/:id/sensors", h.GetSectionSensors)
		api.GET("/sections/:id/realtime", h.GetSectionRealtimeData)
		api.GET("/sections/:id/alerts", h.GetSectionAlerts)
		// 存活感知：拉取某断面下所有传感器的在线状态
		api.GET("/sections/:id/liveness", h.GetSectionLiveness)

		// 传感器相关
		api.GET("/sensors/:id", h.GetSensor)
		api.GET("/sensors/:id/data", h.GetSensorData)
		api.GET("/sensors/:id/rate", h.GetSensorDeformationRate)
		// 存活感知：拉取单台传感器的在线状态
		api.GET("/sensors/:id/liveness", h.GetSensorLiveness)

		// 告警相关
		api.GET("/alerts", h.GetAlerts)
		api.GET("/alerts/active", h.GetActiveAlerts)
		api.PUT("/alerts/:id/resolve", h.ResolveAlert)

		// 概览统计
		api.GET("/dashboard/overview", h.GetDashboardOverview)

		// 调试用：立刻分析某断面的所有传感器（验收脚本使用）
		api.POST("/debug/sections/:id/analyze", h.AnalyzeSectionForTest)
		// 调试用：立刻执行全量存活感知扫描
		api.POST("/debug/detect-offline", h.DebugDetectOffline)
	}
}

// GetSectionLiveness 拉取某断面下所有传感器的在线状态
func (h *Handler) GetSectionLiveness(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	items, err := h.analyzer.GetSensorsLivenessBySection(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "total": len(items)})
}

// GetSensorLiveness 拉取单台传感器的在线状态
func (h *Handler) GetSensorLiveness(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的传感器ID"})
		return
	}
	sensor, err := h.store.GetSensor(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "传感器不存在"})
		return
	}
	items, err := h.analyzer.GetSensorsLivenessBySection(c.Request.Context(), sensor.SectionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, lv := range items {
		if lv.SensorID == id {
			c.JSON(http.StatusOK, gin.H{"data": lv})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "未找到该传感器的存活状态"})
}

// DebugDetectOffline 调试用：立刻执行全量存活感知扫描
//
// 验收/测试场景：停止某个 simulator 上报一段时间后，调用本接口立即触发扫描，
// 不必等待 5 分钟 cron。生产环境慎用——会与 cron 同时抢占 DB 连接。
func (h *Handler) DebugDetectOffline(c *gin.Context) {
	if h.analyzer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analyzer 未注入"})
		return
	}
	h.analyzer.DetectOfflineSensors(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// GetSections 获取所有断面
func (h *Handler) GetSections(c *gin.Context) {
	sections, err := h.store.GetSections(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sections, "total": len(sections)})
}

// GetSection 获取单个断面
func (h *Handler) GetSection(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}

	section, err := h.store.GetSection(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "断面不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": section})
}

// GetSectionSensors 获取断面下所有传感器
func (h *Handler) GetSectionSensors(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}

	sensors, err := h.store.GetSensorsBySection(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sensors, "total": len(sensors)})
}

// GetSectionRealtimeData 获取断面实时数据
func (h *Handler) GetSectionRealtimeData(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}

	section, err := h.store.GetSection(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "断面不存在"})
		return
	}

	latestData, err := h.store.GetLatestSectionData(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	alerts, _ := h.store.GetSectionAlerts(c.Request.Context(), id, 5, string(model.AlertStatusActive))

	dataMap := make(map[int]interface{})
	for _, d := range latestData {
		dataMap[d.SensorID] = d
	}

	// 存活感知：附加每台传感器的在线状态
	// 修复"设备离线但前端仍显示在线"问题——即使 sensor_data 中有历史值，
	// 若最后一次上报超过 stale 阈值，前端必须显示"亚健康/离线"。
	liveness, _ := h.analyzer.GetSensorsLivenessBySection(c.Request.Context(), id)
	livenessMap := make(map[int]model.SensorLiveness, len(liveness))
	for _, lv := range liveness {
		livenessMap[lv.SensorID] = lv
	}

	c.JSON(http.StatusOK, gin.H{
		"section_id":   section.ID,
		"section_code": section.Code,
		"section_name": section.Name,
		"latest_data":  dataMap,
		"alerts":       alerts,
		"liveness":     livenessMap,
		"updated_at":   time.Now(),
	})
}

// GetSectionAlerts 获取断面告警
func (h *Handler) GetSectionAlerts(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// 可选状态过滤：active / resolved / 空（不过滤，兼容历史）
	status := c.Query("status")
	if status != "" && status != string(model.AlertStatusActive) && status != string(model.AlertStatusResolved) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的告警状态"})
		return
	}

	alerts, err := h.store.GetSectionAlerts(c.Request.Context(), id, limit, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": alerts, "total": len(alerts)})
}

// GetSensor 获取传感器信息
func (h *Handler) GetSensor(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的传感器ID"})
		return
	}

	sensor, err := h.store.GetSensor(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "传感器不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sensor})
}

// GetSensorData 获取传感器历史数据
func (h *Handler) GetSensorData(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的传感器ID"})
		return
	}

	// 解析时间范围
	now := time.Now()
	start := now.Add(-24 * time.Hour)
	end := now

	if s := c.Query("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			start = t
		}
	}
	if e := c.Query("end"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			end = t
		}
	}

	limit := 10000
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// 检查是否使用聚合查询
	interval := c.Query("interval")
	if interval != "" {
		data, err := h.store.GetHistoricalDataAggregated(c.Request.Context(), id, start, end, interval)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": data, "total": len(data), "aggregated": true, "interval": interval})
		return
	}

	data, err := h.store.GetHistoricalData(c.Request.Context(), id, start, end, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "total": len(data)})
}

// GetSensorDeformationRate 获取传感器变形速率
func (h *Handler) GetSensorDeformationRate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的传感器ID"})
		return
	}

	rate, err := h.store.CalculateDeformationRate(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rate})
}

// GetAlerts 获取告警列表
func (h *Handler) GetAlerts(c *gin.Context) {
	alerts, err := h.store.GetActiveAlerts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": alerts, "total": len(alerts)})
}

// GetActiveAlerts 获取活跃告警
func (h *Handler) GetActiveAlerts(c *gin.Context) {
	alerts, err := h.store.GetActiveAlerts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": alerts, "total": len(alerts)})
}

// ResolveAlert 解决告警
func (h *Handler) ResolveAlert(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的告警ID"})
		return
	}

	if err := h.store.ResolveAlert(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "告警已解决"})
}

// GetDashboardOverview 获取仪表盘概览
func (h *Handler) GetDashboardOverview(c *gin.Context) {
	sections, err := h.store.GetSections(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	alerts, err := h.store.GetActiveAlerts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 统计告警级别
	dangerCount := 0
	warningCount := 0
	offlineAlertCount := 0
	for _, a := range alerts {
		switch a.Level {
		case model.AlertLevelDanger:
			dangerCount++
		case model.AlertLevelWarning:
			warningCount++
		}
		if a.Type == model.AlertTypeOffline {
			offlineAlertCount++
		}
	}

	// 存活感知：实时统计当前离线的传感器数量（与告警去重周期无关，
	// 只要"最近一次上报超过 30 分钟"就计入离线数）
	offlineSensors, _ := h.countOfflineSensors(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"total_sections":      len(sections),
		"total_alerts":        len(alerts),
		"danger_alerts":       dangerCount,
		"warning_alerts":      warningCount,
		"active_alerts":       len(alerts),
		"offline_sensors":     offlineSensors,
		"offline_alerts":      offlineAlertCount,
	})
}

// countOfflineSensors 实时统计当前处于离线状态的传感器数量
// 用于 Dashboard 概览卡片展示，与"offline_alerts"（告警去重后的活跃数）互为补充：
//   - offline_sensors : 反映"瞬时"离线传感器数（每 5 分钟刷新）
//   - offline_alerts  : 反映"待人工处理"的离线告警数（30 分钟内同级别去重）
func (h *Handler) countOfflineSensors(ctx context.Context) (int, error) {
	pairs, err := h.store.GetSensorsWithSections(ctx)
	if err != nil {
		return 0, err
	}
	if len(pairs) == 0 {
		return 0, nil
	}
	ids := make([]int, 0, len(pairs))
	for _, p := range pairs {
		ids = append(ids, p.SensorID)
	}
	lastDataMap, err := h.store.GetSensorsLastDataAt(ctx, ids)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	offline := 0
	for _, p := range pairs {
		state, _ := store.ComputeSensorState(lastDataMap[p.SensorID], now)
		if state == model.SensorStateOffline || state == model.SensorStateUnknown {
			offline++
		}
	}
	return offline, nil
}

// AnalyzeSectionForTest 触发某断面的全量告警分析（绕过 5 分钟 cron）
//
// 验收脚本 / 联调用：上传异常数据后立刻调用本接口，可以快速验证
// "连续 3 次告警 → 评分下降" 的链路。
func (h *Handler) AnalyzeSectionForTest(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	if h.analyzer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analyzer 未注入"})
		return
	}
	h.analyzer.AnalyzeSectionByID(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"message": "ok", "section_id": id})
}

