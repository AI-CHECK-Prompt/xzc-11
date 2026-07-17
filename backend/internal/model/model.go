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
	ID          int                 `json:"id"`
	Code        string              `json:"code"`
	Name        string              `json:"name"`
	LineCode    string              `json:"line_code"`    // 线路编号
	StationKm   int                 `json:"station_km"`   // 里程位置(米)
	Description string              `json:"description"`
	LocationLat float64             `json:"location_lat"`
	LocationLng float64             `json:"location_lng"`
	PositionType SectionPositionType `json:"position_type"` // 位置类型：station/mid/shaft/cross
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

// RateSource 告警判定来源
type RateSource string

const (
	RateSourceEndpoint   RateSource = "endpoint"   // 端点差值
	RateSourceSlidingWin RateSource = "sliding"    // 滑动窗口最大瞬时速率
	RateSourceStep       RateSource = "step"       // 相邻点阶跃速率
)

// DeformationRate 变形速率计算结果
// 修正历史缺陷：原实现仅取窗口首末两点做差，无法识别中间过程的
// 阶跃跳变与反复波动。Rate 字段为多窗口分析中的"最严值"（绝对值最大），
// 用于告警判定；具体触发来源由 RateSource 标识，诊断信息可回溯到
// EndpointRate / MaxSlidingRate / MaxStepRate。
type DeformationRate struct {
	SensorID   int       `json:"sensor_id"`
	SectionID  int       `json:"section_id"`
	Rate       float64   `json:"rate"`        // 最严速率（mm/天），告警判定用
	RateSource RateSource `json:"rate_source"` // 触发该最严值的来源
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	DataPoints int       `json:"data_points"`
	LastValue  float64   `json:"last_value"`
	FirstValue float64   `json:"first_value"`

	// 端点速率：(lastValue - firstValue) / 实际时间窗 * 24h
	EndpointRate float64 `json:"endpoint_rate"`

	// 滑动窗口最大瞬时速率：固定时长窗口内的最大变化率
	MaxSlidingRate   float64   `json:"max_sliding_rate"`
	SlidingWindow    string    `json:"sliding_window"`     // 窗口时长（可读）
	SlidingStartTime time.Time `json:"sliding_start_time"`
	SlidingEndTime   time.Time `json:"sliding_end_time"`
	SlidingStartVal  float64   `json:"sliding_start_value"`
	SlidingEndVal    float64   `json:"sliding_end_value"`

	// 相邻点阶跃最大速率
	MaxStepRate  float64   `json:"max_step_rate"`
	StepFromTime time.Time `json:"step_from_time"`
	StepToTime   time.Time `json:"step_to_time"`
	StepFromVal  float64   `json:"step_from_value"`
	StepToVal    float64   `json:"step_to_value"`

	// 窗口内的最小/最大值（用于消息中显示瞬时跨度）
	MinValue float64 `json:"min_value"`
	MaxValue float64 `json:"max_value"`
}
