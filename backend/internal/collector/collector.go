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
	hub  *ws.Hub
}

// New 创建采集器
func New(store *store.Store, hub *ws.Hub) *Collector {
	return &Collector{
		store: store,
		hub:  hub,
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

		alerts, _ := c.store.GetSectionAlerts(ctx, sectionID, 5)

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