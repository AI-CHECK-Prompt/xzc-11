package model

import (
	"time"
)

// SensorType 传感器类型
type SensorType string

const (
	SensorTypeDisplacement SensorType = "displacement" // 位移计
	SensorTypeCrack         SensorType = "crack"       // 裂缝计
	SensorTypeStrain        SensorType = "strain"      // 应变计
)

// AlertLevel 告警级别
type AlertLevel string

const (
	AlertLevelInfo    AlertLevel = "info"
	AlertLevelWarning AlertLevel = "warning"
	AlertLevelDanger  AlertLevel = "danger"
)

// AlertStatus 告警状态
type AlertStatus string

const (
	AlertStatusActive   AlertStatus = "active"
	AlertStatusResolved AlertStatus = "resolved"
)

// Section 监测断面
type Section struct {
	ID          int    `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	LineCode    string `json:"line_code"`    // 线路编号
	StationKm   int    `json:"station_km"`   // 里程位置(米)
	Description string `json:"description"`
	LocationLat float64 `json:"location_lat"`
	LocationLng float64 `json:"location_lng"`
}

// Sensor 传感器
type Sensor struct {
	ID         int        `json:"id"`
	SectionID  int        `json:"section_id"`
	Code       string     `json:"code"`
	Type       SensorType `json:"type"`
	Position   string     `json:"position"`  // 安装位置：左侧墙/右侧墙/拱顶
	Calibration float64   `json:"calibration"` // 校准系数
}

// SensorData 传感器时序数据
type SensorData struct {
	ID         int        `json:"id"`
	SensorID   int        `json:"sensor_id"`
	Value      float64    `json:"value"`
	Timestamp  time.Time  `json:"timestamp"`
}

// SensorDataBatch 批量上传的数据
type SensorDataBatch struct {
	CollectorCode string        `json:"collector_code"`
	Data          []SensorData `json:"data"`
}

// Alert 告警记录
type Alert struct {
	ID           int           `json:"id"`
	SectionID    int           `json:"section_id"`
	SensorID     int           `json:"sensor_id"`
	Level        AlertLevel    `json:"level"`
	Message      string        `json:"message"`
	DeformationRate float64   `json:"deformation_rate"`
	Threshold    float64       `json:"threshold"`
	Status       AlertStatus   `json:"status"`
	TriggeredAt  time.Time     `json:"triggered_at"`
	ResolvedAt   *time.Time    `json:"resolved_at"`
}

// SectionRealtimeData 断面实时数据
type SectionRealtimeData struct {
	SectionID   int                   `json:"section_id"`
	SectionCode string                `json:"section_code"`
	SectionName string                `json:"section_name"`
	LatestData  map[int]SensorData    `json:"latest_data"`
	Alerts      []Alert               `json:"alerts"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

// DeformationRate 变形速率计算结果
type DeformationRate struct {
	SensorID     int       `json:"sensor_id"`
	SectionID    int       `json:"section_id"`
	Rate         float64   `json:"rate"` // 单位：mm/天
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	DataPoints   int       `json:"data_points"`
	LastValue    float64   `json:"last_value"`
	FirstValue   float64   `json:"first_value"`
}
