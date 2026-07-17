package model

import "time"

// HealthGrade 健康度分级
type HealthGrade string

const (
	HealthGradeExcellent HealthGrade = "excellent" // 优良 ≥90
	HealthGradeNormal    HealthGrade = "normal"    // 正常 75-89
	HealthGradeAttention HealthGrade = "attention" // 关注 60-74
	HealthGradeDegraded  HealthGrade = "degraded"  // 劣化 40-59
	HealthGradeDanger    HealthGrade = "danger"    // 危险 <40
)

// HealthGradeThresholds 分级阈值（下界包含，上界不含）。
// 顺序必须按 MinScore 升序，否则 GradeFromScore 逻辑会出错。
var HealthGradeThresholds = []struct {
	Grade    HealthGrade
	MinScore float64
}{
	{HealthGradeDanger, 0},
	{HealthGradeDegraded, 40},
	{HealthGradeAttention, 60},
	{HealthGradeNormal, 75},
	{HealthGradeExcellent, 90},
}

// GradeFromScore 根据分值返回等级。负数钳制为 0，超过 100 钳制为 100。
func GradeFromScore(score float64) HealthGrade {
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	g := HealthGradeDanger
	for _, t := range HealthGradeThresholds {
		if score >= t.MinScore {
			g = t.Grade
		}
	}
	return g
}

// SectionPositionType 断面位置类型
type SectionPositionType string

const (
	PositionStation SectionPositionType = "station" // 车站区域
	PositionMid     SectionPositionType = "mid"     // 区间中部（默认）
	PositionShaft   SectionPositionType = "shaft"   // 风井区域
	PositionCross   SectionPositionType = "cross"   // 联络通道
)

// PositionSensitivity 位置敏感度系数（>1 表示更敏感，分扣更多）
var PositionSensitivity = map[SectionPositionType]float64{
	PositionStation: 1.0,
	PositionMid:     1.0,
	PositionShaft:   1.1,
	PositionCross:   1.2,
}

// SensorTypeWeight 传感器类型权重（聚合到断面时使用）
var SensorTypeWeight = map[SensorType]float64{
	SensorTypeDisplacement: 0.40,
	SensorTypeCrack:        0.35,
	SensorTypeStrain:       0.25,
}

// DimensionWeights 评分维度权重（每个传感器子分内部使用）
var DimensionWeights = struct {
	Alert        float64
	Trend        float64
	Stability    float64
	Completeness float64
}{
	Alert:        0.40,
	Trend:        0.30,
	Stability:    0.20,
	Completeness: 0.10,
}

// HealthTriggerType 评分触发类型
type HealthTriggerType string

const (
	HealthTriggerCron  HealthTriggerType = "cron"
	HealthTriggerEvent HealthTriggerType = "event"
)

// SectionHealthScore 一次断面健康度评分记录
type SectionHealthScore struct {
	ID                         int                 `json:"id"`
	SectionID                  int                 `json:"section_id"`
	TotalScore                 float64             `json:"total_score"`
	Grade                      HealthGrade         `json:"grade"`
	DisplacementScore          float64             `json:"displacement_score"`
	CrackScore                 float64             `json:"crack_score"`
	StrainScore                float64             `json:"strain_score"`
	AlertDimensionScore        float64             `json:"alert_dimension_score"`
	TrendDimensionScore        float64             `json:"trend_dimension_score"`
	StabilityDimensionScore    float64             `json:"stability_dimension_score"`
	CompletenessDimensionScore float64             `json:"completeness_dimension_score"`
	PositionType               SectionPositionType `json:"position_type"`
	Sensitivity                float64             `json:"sensitivity"`
	TriggerType                HealthTriggerType   `json:"trigger_type"`
	CalculatedAt               time.Time           `json:"calculated_at"`
}

// ScoreDetail 评分明细（可解释）
type ScoreDetail struct {
	ID           int64     `json:"id"`
	ScoreID      int64     `json:"score_id"`
	SectionID    int       `json:"section_id"`
	Dimension    string    `json:"dimension"`
	SubDimension string    `json:"sub_dimension"`
	RawValue     float64   `json:"raw_value"`
	SubScore     float64   `json:"sub_score"`
	Weight       float64   `json:"weight"`
	Contribution float64   `json:"contribution"`
	Explanation  string    `json:"explanation"`
	CalculatedAt time.Time `json:"calculated_at"`
}

// ScoreIntermediate 复核中间数据
type ScoreIntermediate struct {
	ID                 int64      `json:"id"`
	ScoreID            int64      `json:"score_id"`
	SectionID          int        `json:"section_id"`
	SensorID           int        `json:"sensor_id"`
	SensorType         SensorType `json:"sensor_type"`
	Rate24h            float64    `json:"rate_24h"`
	Rate7d             float64    `json:"rate_7d"`
	Rate30d            float64    `json:"rate_30d"`
	RecentAlertCount   int        `json:"recent_alert_count"`
	DataCompleteness   float64    `json:"data_completeness"`
	HistoricalVariance float64    `json:"historical_variance"`
	SensorSubScore     float64    `json:"sensor_sub_score"`
	InputsJSON         string     `json:"inputs_json"`
	CalculatedAt       time.Time  `json:"calculated_at"`
}
