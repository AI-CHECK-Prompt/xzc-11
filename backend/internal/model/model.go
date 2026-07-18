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

// 告警处理人常量
//
// 用于在数据库 alerts.handler 字段中标识告警的处置来源，
// 便于安全例会按"处理人"统计每位运维的告警处置工作量。
//
//   - AlertHandlerSystem : 系统自动恢复（cron 判定数据已恢复、无人介入）
//   - AlertHandlerUnknown : 处理人未知（历史数据 / 接口调用未携带用户信息时兜底）
const (
	AlertHandlerSystem  = "system"
	AlertHandlerUnknown = "unknown"
)

// ContextUserKey gin.Context 中存放当前用户名的 key
//
// 业务 handler 通过 model.GetCurrentUser(c) 读取当前操作者（运维账号）。
// 写入由 main.go 的 userContextMiddleware 负责（从 X-User 头取值）。
const ContextUserKey = "user"

// GetCurrentUser 从 gin.Context 读取当前操作者账号
//
// 优先返回值：
//   1. 中间件写入的 user 字段（来自 X-User / Authorization）
//   2. 兜底返回 AlertHandlerUnknown
//
// 之所以用 model 包封装而不是散落在 handler 里直接 c.Get("user")，
// 是为了：
//   - 收敛 key 字符串，避免 key 拼写漂移
//   - 默认值处理集中在一处，所有 handler 行为一致
//   - 后续若切换到 SSO/JWT，只需修改本函数实现
func GetCurrentUser(c interface{ GetString(string) string }) string {
	if u := c.GetString(ContextUserKey); u != "" {
		return u
	}
	return AlertHandlerUnknown
}

// AlertType 告警类型
// 区分告警触发原因，便于前端分类展示与告警引擎覆盖范围分析。
//   - AlertTypeRate  : 变形速率超阈值（原始告警类型，向后兼容缺省值）
//   - AlertTypeOffline: 设备离线 / 数据缺失（存活感知）
type AlertType string

const (
	AlertTypeRate    AlertType = "rate"     // 速率超阈值告警（默认类型）
	AlertTypeOffline AlertType = "offline"  // 设备离线 / 数据缺失告警
)

// SensorOnlineState 传感器在线状态
type SensorOnlineState string

const (
	SensorStateOnline  SensorOnlineState = "online"  // 在线（最近一次数据在 10 分钟内）
	SensorStateStale   SensorOnlineState = "stale"   // 亚健康（最近一次数据在 [10min, 30min)）
	SensorStateOffline SensorOnlineState = "offline" // 离线（最近一次数据 >= 30min）
	SensorStateUnknown SensorOnlineState = "unknown" // 未知（从未上报过数据）
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
//
// Handler 字段记录告警的处置人：
//   - 人工解决：填写具体运维账号（前端 X-User 头传入）
//   - 系统自动恢复：固定为 model.AlertHandlerSystem ("system")
//   - 兜底/历史：固定为 model.AlertHandlerUnknown ("unknown")
//
// 之所以在模型层定义常量，是为了在 store / api / analyzer 多个包内
// 复用同一份字符串，避免出现 "system" / "System" / "auto" 等拼写漂移
// 导致按"处理人"统计时同一类来源被拆成多组。
type Alert struct {
	ID           int           `json:"id"`
	SectionID    int           `json:"section_id"`
	SensorID     int           `json:"sensor_id"`
	Level        AlertLevel    `json:"level"`
	Type         AlertType     `json:"type"`            // 告警类型（rate/offline），缺省 rate
	Message      string        `json:"message"`
	DeformationRate float64   `json:"deformation_rate"`
	Threshold    float64       `json:"threshold"`
	Status       AlertStatus   `json:"status"`
	TriggeredAt  time.Time     `json:"triggered_at"`
	ResolvedAt   *time.Time    `json:"resolved_at"`
	// Handler 处置人（运维账号 / system / unknown）。
	// 使用指针以便在 JSON 序列化时区分"未填写（nil）"与"空字符串"——
	// 前端展示时 nil 显示为 "-"，"" 视为异常数据，便于排查。
	Handler      *string       `json:"handler,omitempty"`
}

// SensorLiveness 传感器存活/在线状态
// 用于存活感知接口，告诉前端"这台设备是否还活着"。
//   - LastDataAt : 最近一次上报时间（NULL 表示从未上报）
//   - State      : online / stale / offline / unknown
//   - MinutesSinceLastData : 距上次上报的分钟数，-1 表示从未上报
//   - ExpectedIntervalMin  : 期望上报周期（分钟），用于前端展示
type SensorLiveness struct {
	SensorID            int               `json:"sensor_id"`
	SectionID           int               `json:"section_id"`
	LastDataAt          *time.Time        `json:"last_data_at"`
	State               SensorOnlineState `json:"state"`
	MinutesSinceLastData int              `json:"minutes_since_last_data"`
	ExpectedIntervalMin  int              `json:"expected_interval_min"`
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
