package collector

import (
	"context"
	"log"
	"net/http"
	"time"
	"tunnel-shm/internal/model"
	"tunnel-shm/internal/store"
	"tunnel-shm/internal/ws"

	"github.com/gin-gonic/gin"
)

// Collector 数据采集服务
type Collector struct {
	store *store.Store
	hub   *ws.Hub
}

// New 创建采集器
func New(store *store.Store, hub *ws.Hub) *Collector {
	return &Collector{
		store: store,
		hub:   hub,
	}
}

// HandleCollectData 处理传感器数据上报
// POST /api/v1/collect
func (c *Collector) HandleCollectData(ctx *gin.Context) {
	var batch model.SensorDataBatch
	if err := ctx.ShouldBindJSON(&batch); err != nil {
		log.Printf("【采集-错误】解析请求失败: %v", err)
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "数据格式错误", "detail": err.Error()})
		return
	}

	if len(batch.Data) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "数据为空"})
		return
	}

	// 补充时间戳
	now := time.Now()
	for i := range batch.Data {
		if batch.Data[i].Timestamp.IsZero() {
			batch.Data[i].Timestamp = now
		}
	}

	// 应用每台传感器独立配置的校准系数
	// 修复 bug：原实现直接写入原始值，导致出厂系数（如 1.05）形同虚设，
	// 与人工实测值出现 ~5% 系统性偏差，影响长期趋势分析。
	// 策略：按本批次涉及的 sensorID 一次性批量查询 calibration，
	// 然后在原值上做乘法修正；未配置或缺失的传感器按 1.0 处理（保持原行为）。
	if err := c.applyCalibration(ctx.Request.Context(), batch.Data); err != nil {
		log.Printf("【采集-错误】加载校准系数失败: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "校准系数加载失败"})
		return
	}

	// 批量写入数据库
	startTime := time.Now()
	if err := c.store.InsertSensorDataBatch(ctx.Request.Context(), batch.Data); err != nil {
		log.Printf("【采集-错误】数据写入失败: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "数据存储失败"})
		return
	}

	elapsed := time.Since(startTime)
	log.Printf("【采集-处理】采集器=%s, 数据条数=%d, 耗时=%v",
		batch.CollectorCode, len(batch.Data), elapsed)

	// 推送实时数据到WebSocket
	c.pushRealtimeData(ctx.Request.Context(), batch.Data)

	ctx.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"count":   len(batch.Data),
	})
}

// applyCalibration 按 sensors 表中的 calibration 系数对原始值做修正
// 缺省系数 1.0 保持原值不变（兼容历史/未配置传感器）。
func (c *Collector) applyCalibration(ctx context.Context, data []model.SensorData) error {
	idSet := make(map[int]struct{}, len(data))
	ids := make([]int, 0, len(data))
	for _, d := range data {
		if _, ok := idSet[d.SensorID]; ok {
			continue
		}
		idSet[d.SensorID] = struct{}{}
		ids = append(ids, d.SensorID)
	}

	calMap, err := c.store.GetSensorsCalibrationByIDs(ctx, ids)
	if err != nil {
		return err
	}

	applyCalibrationMap(data, calMap)
	return nil
}

// applyCalibrationMap 纯函数：按 calMap 对每条数据做乘法修正
// 缺校/未配置/不在 map 中的 sensorID 保持原值（与原行为一致）。
// 抽取为纯函数以便单元测试：可注入任意 calMap，验证入库前的值是否正确。
func applyCalibrationMap(data []model.SensorData, calMap map[int]float64) {
	for i := range data {
		cal, ok := calMap[data[i].SensorID]
		if !ok {
			continue
		}
		data[i].Value = data[i].Value * cal
	}
}

// pushRealtimeData 推送实时数据到WebSocket
func (c *Collector) pushRealtimeData(ctx context.Context, data []model.SensorData) {
	// 按断面分组
	sectionIDs := make(map[int]bool)
	sensorIDs := make(map[int]bool)
	for _, d := range data {
		sensorIDs[d.SensorID] = true
	}

	// 获取传感器所属断面
	sectionMap := make(map[int]int) // sensorID -> sectionID
	for sensorID := range sensorIDs {
		sensor, err := c.store.GetSensor(ctx, sensorID)
		if err != nil {
			continue
		}
		sectionMap[sensorID] = sensor.SectionID
		sectionIDs[sensor.SectionID] = true
	}

	// 为每个断面推送实时数据
	for sectionID := range sectionIDs {
		section, err := c.store.GetSection(ctx, sectionID)
		if err != nil {
			continue
		}

		latestData, err := c.store.GetLatestSectionData(ctx, sectionID)
		if err != nil {
			continue
		}

		alerts, _ := c.store.GetSectionAlerts(ctx, sectionID, 5, string(model.AlertStatusActive))

		dataMap := make(map[int]model.SensorData)
		for _, d := range latestData {
			dataMap[d.SensorID] = d
		}

		realtime := &model.SectionRealtimeData{
			SectionID:   sectionID,
			SectionCode: section.Code,
			SectionName: section.Name,
			LatestData:  dataMap,
			Alerts:      alerts,
			UpdatedAt:   time.Now(),
		}

		c.hub.BroadcastDataUpdate(realtime)
	}
}

// HandleHealthCheck 健康检查
func (c *Collector) HandleHealthCheck(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}
