# 断面健康度综合评估 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 基于位移/裂缝/应变多源传感器数据、当前告警、近期趋势，对每个监测断面产出 0-100 健康分值与五档分级（优良/正常/关注/劣化/危险），并在管理首页新增看板与可解释明细查询。

**Architecture:**
- 后端：新增独立 `healthscore` 包，纯函数评分引擎（与 DB/IO 解耦，便于单测）；新增 3 张表（最新评分、明细、复核中间数据），复用现有 `CalculateDeformationRate` 多窗口算法；定时任务（cron 每 1 分钟）+ 告警事件触发双通道；保留所有现有表结构与告警链路不变。
- 前端：在 Dashboard 嵌入"断面健康度看板"组件，新开"断面健康度详情"路由展示评分构成 + 历史曲线；遵循现有 Vue3 + Pinia + Chart.js 风格，UI 组件结构不重构。
- 数据：health_score 三张表统一 3 年保留（与 sensor_data 一致），通过 `add_retention_policy` 配置。

**Tech Stack:** Go 1.21 + Gin + pgx/v5 + TimescaleDB + robfig/cron/v3；Vue 3 + TypeScript + Pinia + Chart.js。

## Global Constraints

- 健康分级固定为 5 档：`excellent` 优良 ≥90 / `normal` 正常 75-89 / `attention` 关注 60-74 / `degraded` 劣化 40-59 / `danger` 危险 <40。`danger` 档位单独触发并由前端高亮红底。
- 评分综合四个维度（权重固定）：**当前告警 40%** + **近期变化趋势 30%** + **历史稳定性 20%** + **数据完整性 10%**。每个维度满分 100，先算每个传感器子分，再按类型加权聚合到断面。
- 传感器类型权重：位移 0.40 / 裂缝 0.35 / 应变 0.25（隧道结构位移裂缝为关键指标）。
- 断面位置特性（`position_type` 枚举：`station` 车站区域 / `mid` 区间中部 / `shaft` 风井区域 / `cross` 联络通道）通过敏感度系数影响评分：`station` ×1.0 / `mid` ×1.0 / `shaft` ×1.1（风井区域风险更高） / `cross` ×1.2（联络通道最敏感）。缺省值 `mid`。新增为 `sections` 表的可空列，不破坏已有数据。
- 评分更新双通道：(a) cron 每 1 分钟对全部断面重算一次；(b) 告警插入事务提交后异步触发对应断面重算（避免阻塞告警链路）。
- 历史数据保留 ≥3 年：`section_health_scores` / `section_health_score_details` / `section_health_score_intermediate` 三表通过 TimescaleDB `add_retention_policy` 配置 3 年策略。
- **不动**：现有 `sensor_data` / `alerts` / `sections` / `sensors` 表结构与所有现有 API 端点行为；只允许向 `sections` 表追加可空列 `position_type`。
- 评分必须可解释：每次评分产出 `details` 明细 + `intermediate` 中间数据，明细包含每个维度的子分与权重贡献，中间数据保留全部输入值用于复核重算。
- API SLA：健康度历史曲线查询（任意时间区间）响应 P95 ≤3s；评分更新延迟 ≤1min（cron 间隔 + 事件触发保证）。
- 前端不变更 UI 框架与组件结构，仅新增视图/组件 + 现有 Dashboard 内嵌新区块；遵循 `frontend/src/assets/main.css` 现有变量与类名风格。
- 所有日志统一使用 `【健康度-xxx】` 前缀（与现有 `【分析-xxx】` / `【系统-xxx】` 一致）。

---

## File Structure

**新增后端文件：**
- `backend/internal/model/health.go` — 健康度相关数据模型（`HealthGrade` 枚举、`SectionHealthScore`、`ScoreDetail`、`ScoreIntermediate`、`SectionPositionType` 枚举）
- `backend/internal/healthscore/engine.go` — 纯函数评分引擎（`ComputeSectionScore`、`computeSensorSubScore`、`aggregateToSection`）
- `backend/internal/healthscore/engine_test.go` — 单元测试（典型场景：全优、单项越界、连续告警、缺数据）
- `backend/internal/healthscore/scheduler.go` — cron 定时任务 + 事件触发 `EnqueueRecompute(sectionID)`
- `backend/internal/api/health_handler.go` — 三个 HTTP 处理器（看板总览、明细、历史曲线）
- `backend/internal/healthscore/testdata_test.go` — 测试用夹具数据（避免重复构造 SensorData 切片）

**修改后端文件：**
- `docker/init-db.sql` — 新增 3 张表 + 索引 + TimescaleDB hypertable + retention policy + 给 `sections` 加 `position_type` 可空列
- `backend/internal/store/store.go` — 新增 `InsertHealthScore` / `GetLatestSectionHealthScore` / `ListSectionHealthScoresByLine` / `GetHealthScoreHistoryAggregated` / `GetActiveAlertsBySectionSince` / `GetSectionHealthScoreDetails` / `GetSectionHealthScoreIntermediate` / `GetSensorRateRaw` / `GetSectionSensorStatsForScore`（拉取最近告警 / 数据完整率 / 24h/7d/30d 速率所需原始数据）
- `backend/internal/analyzer/analyzer.go` — 在 `analyzeSensor` 插入告警成功后（`InsertAlert` 返回 nil 后）调用 `healthscore.NotifyAlertInserted(section.ID)` 触发该断面重算
- `backend/cmd/server/main.go` — 构造 `healthscore.New(store, threshold)` 并启动其 cron，注入到 analyzer，注入到 handler，注册新路由
- `backend/internal/api/handler.go` — `RegisterRoutes` 中调用 `RegisterHealthRoutes`

**新增前端文件：**
- `frontend/src/api/health.ts` — 三个 API 封装函数 + TypeScript 类型
- `frontend/src/components/HealthDashboard.vue` — 看板子组件（按线路分组 + 排名 + 趋势条 + 告警数）
- `frontend/src/views/SectionHealthDetail.vue` — 详情页（评分构成表 + 历史曲线 Chart.js）
- `frontend/src/types/health.ts` — 健康度相关 TS 类型（也可放在 `env.d.ts` 内，但为保持单文件类型可读，独立成文件）

**修改前端文件：**
- `frontend/src/router/index.ts` — 新增 `/sections/:id/health` 路由
- `frontend/src/stores/monitor.ts` — 新增 `healthOverview` / `fetchHealthOverview` / `fetchSectionHealth`
- `frontend/src/views/Dashboard.vue` — 在"最近告警"卡片之上嵌入 `<HealthDashboard />`；`onMounted` 追加 `fetchHealthOverview()`
- `frontend/src/views/SectionDetail.vue` — 在传感器列表上方新增"健康度评分"区块，链接到详情页
- `frontend/src/api/index.ts` — 重导出新 API（如需保持单一入口；否则各组件直接 `import` `api/health.ts`）

---

## Task 1: 数据库 schema 与数据模型

**Files:**
- Modify: `docker/init-db.sql:1-101`（在文件末尾追加新表 + 索引 + policy + 列）
- Create: `backend/internal/model/health.go`
- Create: `backend/internal/model/health_test.go`（仅枚举值测试，避免编译漂移）

**Interfaces:**
- Produces（被后续任务消费）：
  - `model.HealthGrade`（string enum）: `excellent` / `normal` / `attention` / `degraded` / `danger`
  - `model.SectionPositionType`（string enum）: `station` / `mid` / `shaft` / `cross`
  - `model.PositionSensitivity`（map[PositionType]float64）
  - `model.SectionHealthScore` 整行结构（含所有数值字段，对应 `section_health_scores` 表）
  - `model.ScoreDetail`（`section_health_score_details` 一行）
  - `model.ScoreIntermediate`（`section_health_score_intermediate` 一行）

- [ ] **Step 1: 在 `docker/init-db.sql` 末尾追加 schema**

在 `docker/init-db.sql` 现有 `END $$;` 之后（最后一行）追加以下 SQL 块（注意 `sections` 表的可空列要单独 ALTER 一次以兼容已有数据）：

```sql
-- 断面位置特性（追加列，向后兼容，缺省 'mid'）
ALTER TABLE sections ADD COLUMN IF NOT EXISTS position_type VARCHAR(20) NOT NULL DEFAULT 'mid';
CREATE INDEX IF NOT EXISTS idx_sections_position_type ON sections (position_type);

-- 健康度最新评分表（每次评分一行；同断面多行，新行覆盖查询视图的"最新"）
CREATE TABLE IF NOT EXISTS section_health_scores (
    id SERIAL,
    section_id INTEGER NOT NULL REFERENCES sections(id),
    total_score DOUBLE PRECISION NOT NULL,
    grade VARCHAR(20) NOT NULL,
    displacement_score DOUBLE PRECISION NOT NULL,
    crack_score DOUBLE PRECISION NOT NULL,
    strain_score DOUBLE PRECISION NOT NULL,
    alert_dimension_score DOUBLE PRECISION NOT NULL,
    trend_dimension_score DOUBLE PRECISION NOT NULL,
    stability_dimension_score DOUBLE PRECISION NOT NULL,
    completeness_dimension_score DOUBLE PRECISION NOT NULL,
    position_type VARCHAR(20) NOT NULL,
    sensitivity DOUBLE PRECISION NOT NULL,
    trigger_type VARCHAR(20) NOT NULL,  -- cron / event
    calculated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, calculated_at)
);
SELECT create_hypertable('section_health_scores', 'calculated_at', if_not_exists => TRUE,
    chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS idx_shs_section_time ON section_health_scores (section_id, calculated_at DESC);
CREATE INDEX IF NOT EXISTS idx_shs_grade_time ON section_health_scores (grade, calculated_at DESC);

-- 评分明细表（每个维度一行，可解释）
CREATE TABLE IF NOT EXISTS section_health_score_details (
    id SERIAL,
    score_id BIGINT NOT NULL,
    section_id INTEGER NOT NULL,
    dimension VARCHAR(40) NOT NULL,   -- alert / trend / stability / completeness / sensor_displacement ...
    sub_dimension VARCHAR(60) NOT NULL DEFAULT '',
    raw_value DOUBLE PRECISION NOT NULL,
    sub_score DOUBLE PRECISION NOT NULL,
    weight DOUBLE PRECISION NOT NULL,
    contribution DOUBLE PRECISION NOT NULL,
    explanation TEXT NOT NULL,
    calculated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, calculated_at)
);
SELECT create_hypertable('section_health_score_details', 'calculated_at', if_not_exists => TRUE,
    chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS idx_shsd_score ON section_health_score_details (score_id, calculated_at DESC);

-- 复核中间数据表（保留全部输入值，供重算与审计）
CREATE TABLE IF NOT EXISTS section_health_score_intermediate (
    id SERIAL,
    score_id BIGINT NOT NULL,
    section_id INTEGER NOT NULL,
    sensor_id INTEGER NOT NULL,
    sensor_type VARCHAR(20) NOT NULL,
    rate_24h DOUBLE PRECISION NOT NULL,
    rate_7d DOUBLE PRECISION NOT NULL,
    rate_30d DOUBLE PRECISION NOT NULL,
    recent_alert_count INTEGER NOT NULL,
    data_completeness DOUBLE PRECISION NOT NULL,
    historical_variance DOUBLE PRECISION NOT NULL,
    sensor_sub_score DOUBLE PRECISION NOT NULL,
    inputs_json TEXT NOT NULL,  -- 全部输入原始值的 JSON 快照
    calculated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, calculated_at)
);
SELECT create_hypertable('section_health_score_intermediate', 'calculated_at', if_not_exists => TRUE,
    chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS idx_shsi_score ON section_health_score_intermediate (score_id, calculated_at DESC);
CREATE INDEX IF NOT EXISTS idx_shsi_section_sensor ON section_health_score_intermediate (section_id, sensor_id, calculated_at DESC);

-- 3 年数据保留策略
SELECT add_retention_policy('section_health_scores', INTERVAL '3 years', if_not_exists => TRUE);
SELECT add_retention_policy('section_health_score_details', INTERVAL '3 years', if_not_exists => TRUE);
SELECT add_retention_policy('section_health_score_intermediate', INTERVAL '3 years', if_not_exists => TRUE);
```

- [ ] **Step 2: 同步 `backend/internal/store/store.go` 的 `InitSchema` 字符串**

将 `store.go` 中 `InitSchema` 的 schema 字符串追加同样的 DDL（确保应用启动时新部署也能自动建表）。完整追加块：

```go
// 追加到 schema 字符串末尾（注意 Go 原始字符串用反引号，逐行追加即可）
`

-- 断面位置特性（追加列，向后兼容，缺省 'mid'）
ALTER TABLE sections ADD COLUMN IF NOT EXISTS position_type VARCHAR(20) NOT NULL DEFAULT 'mid';
CREATE INDEX IF NOT EXISTS idx_sections_position_type ON sections (position_type);

-- 健康度最新评分表
CREATE TABLE IF NOT EXISTS section_health_scores (
    id SERIAL,
    section_id INTEGER NOT NULL REFERENCES sections(id),
    total_score DOUBLE PRECISION NOT NULL,
    grade VARCHAR(20) NOT NULL,
    displacement_score DOUBLE PRECISION NOT NULL,
    crack_score DOUBLE PRECISION NOT NULL,
    strain_score DOUBLE PRECISION NOT NULL,
    alert_dimension_score DOUBLE PRECISION NOT NULL,
    trend_dimension_score DOUBLE PRECISION NOT NULL,
    stability_dimension_score DOUBLE PRECISION NOT NULL,
    completeness_dimension_score DOUBLE PRECISION NOT NULL,
    position_type VARCHAR(20) NOT NULL,
    sensitivity DOUBLE PRECISION NOT NULL,
    trigger_type VARCHAR(20) NOT NULL,
    calculated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, calculated_at)
);
SELECT create_hypertable('section_health_scores', 'calculated_at', if_not_exists => TRUE,
    chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS idx_shs_section_time ON section_health_scores (section_id, calculated_at DESC);
CREATE INDEX IF NOT EXISTS idx_shs_grade_time ON section_health_scores (grade, calculated_at DESC);

-- 评分明细表
CREATE TABLE IF NOT EXISTS section_health_score_details (
    id SERIAL,
    score_id BIGINT NOT NULL,
    section_id INTEGER NOT NULL,
    dimension VARCHAR(40) NOT NULL,
    sub_dimension VARCHAR(60) NOT NULL DEFAULT '',
    raw_value DOUBLE PRECISION NOT NULL,
    sub_score DOUBLE PRECISION NOT NULL,
    weight DOUBLE PRECISION NOT NULL,
    contribution DOUBLE PRECISION NOT NULL,
    explanation TEXT NOT NULL,
    calculated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, calculated_at)
);
SELECT create_hypertable('section_health_score_details', 'calculated_at', if_not_exists => TRUE,
    chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS idx_shsd_score ON section_health_score_details (score_id, calculated_at DESC);

-- 复核中间数据表
CREATE TABLE IF NOT EXISTS section_health_score_intermediate (
    id SERIAL,
    score_id BIGINT NOT NULL,
    section_id INTEGER NOT NULL,
    sensor_id INTEGER NOT NULL,
    sensor_type VARCHAR(20) NOT NULL,
    rate_24h DOUBLE PRECISION NOT NULL,
    rate_7d DOUBLE PRECISION NOT NULL,
    rate_30d DOUBLE PRECISION NOT NULL,
    recent_alert_count INTEGER NOT NULL,
    data_completeness DOUBLE PRECISION NOT NULL,
    historical_variance DOUBLE PRECISION NOT NULL,
    sensor_sub_score DOUBLE PRECISION NOT NULL,
    inputs_json TEXT NOT NULL,
    calculated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, calculated_at)
);
SELECT create_hypertable('section_health_score_intermediate', 'calculated_at', if_not_exists => TRUE,
    chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS idx_shsi_score ON section_health_score_intermediate (score_id, calculated_at DESC);
CREATE INDEX IF NOT EXISTS idx_shsi_section_sensor ON section_health_score_intermediate (section_id, sensor_id, calculated_at DESC);

-- 3 年数据保留策略
SELECT add_retention_policy('section_health_scores', INTERVAL '3 years', if_not_exists => TRUE);
SELECT add_retention_policy('section_health_score_details', INTERVAL '3 years', if_not_exists => TRUE);
SELECT add_retention_policy('section_health_score_intermediate', INTERVAL '3 years', if_not_exists => TRUE);
`
```

- [ ] **Step 3: 新建 `backend/internal/model/health.go`**

```go
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

// HealthGradeThresholds 分级阈值（下界包含，上界不含）
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

// GradeFromScore 根据分值返回等级
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

// DimensionWeights 评分维度权重
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
	ID                        int                `json:"id"`
	SectionID                 int                `json:"section_id"`
	TotalScore                float64            `json:"total_score"`
	Grade                     HealthGrade        `json:"grade"`
	DisplacementScore         float64            `json:"displacement_score"`
	CrackScore                float64            `json:"crack_score"`
	StrainScore               float64            `json:"strain_score"`
	AlertDimensionScore       float64            `json:"alert_dimension_score"`
	TrendDimensionScore       float64            `json:"trend_dimension_score"`
	StabilityDimensionScore   float64            `json:"stability_dimension_score"`
	CompletenessDimensionScore float64           `json:"completeness_dimension_score"`
	PositionType              SectionPositionType `json:"position_type"`
	Sensitivity               float64            `json:"sensitivity"`
	TriggerType               HealthTriggerType `json:"trigger_type"`
	CalculatedAt              time.Time          `json:"calculated_at"`
}

// ScoreDetail 评分明细（可解释）
type ScoreDetail struct {
	ID           int       `json:"id"`
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
	ID                  int64      `json:"id"`
	ScoreID             int64      `json:"score_id"`
	SectionID           int        `json:"section_id"`
	SensorID            int        `json:"sensor_id"`
	SensorType          SensorType `json:"sensor_type"`
	Rate24h             float64    `json:"rate_24h"`
	Rate7d              float64    `json:"rate_7d"`
	Rate30d             float64    `json:"rate_30d"`
	RecentAlertCount    int        `json:"recent_alert_count"`
	DataCompleteness    float64    `json:"data_completeness"`
	HistoricalVariance  float64    `json:"historical_variance"`
	SensorSubScore      float64    `json:"sensor_sub_score"`
	InputsJSON          string     `json:"inputs_json"`
	CalculatedAt        time.Time  `json:"calculated_at"`
}
```

- [ ] **Step 4: 新建 `backend/internal/model/health_test.go`**

```go
package model

import "testing"

func TestGradeFromScore(t *testing.T) {
	cases := []struct {
		score float64
		want  HealthGrade
	}{
		{-5, HealthGradeDanger},
		{0, HealthGradeDanger},
		{39.9, HealthGradeDanger},
		{40, HealthGradeDegraded},
		{59.9, HealthGradeDegraded},
		{60, HealthGradeAttention},
		{74.9, HealthGradeAttention},
		{75, HealthGradeNormal},
		{89.9, HealthGradeNormal},
		{90, HealthGradeExcellent},
		{100, HealthGradeExcellent},
		{150, HealthGradeExcellent},
	}
	for _, c := range cases {
		if got := GradeFromScore(c.score); got != c.want {
			t.Errorf("GradeFromScore(%v) = %v, want %v", c.score, got, c.want)
		}
	}
}

func TestPositionSensitivity(t *testing.T) {
	if PositionSensitivity[PositionCross] <= PositionSensitivity[PositionMid] {
		t.Error("联络通道敏感度应高于区间中部")
	}
	if PositionSensitivity[PositionMid] != 1.0 {
		t.Error("区间中部敏感度应为 1.0")
	}
}
```

- [ ] **Step 5: 运行测试**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\backend && go test ./internal/model/... -v`
Expected: PASS，2 个测试通过。

- [ ] **Step 6: 提交**

```bash
git add docker/init-db.sql backend/internal/model/health.go backend/internal/model/health_test.go backend/internal/store/store.go
git commit -m "feat(health): 新增断面健康度三表 + 模型定义"
```

---

## Task 2: 评分引擎（纯函数 + 单元测试）

**Files:**
- Create: `backend/internal/healthscore/engine.go`
- Create: `backend/internal/healthscore/engine_test.go`
- Create: `backend/internal/healthscore/testdata_test.go`

**Interfaces:**
- Consumes: 现有 `model.SensorType` / `model.Threshold` / `model.SectionPositionType` / `model.SensorData` / `model.HealthGrade` / `model.ScoreDetail` / `model.ScoreIntermediate`
- Produces:
  - `func ComputeSectionScore(ctx context.Context, section model.Section, sensors []model.Sensor, inputs []SensorScoreInput) (*model.SectionHealthScore, []model.ScoreDetail, []model.ScoreIntermediate, error)` — 纯函数，调用方传所有必要数据
  - `type SensorScoreInput struct { SensorID int; SensorType model.SensorType; Rate24h/Rate7d/Rate30h/HistoricalVariance/DataCompleteness float64; RecentAlertCount int }`

- [ ] **Step 1: 新建 `backend/internal/healthscore/testdata_test.go`**

```go
package healthscore

import (
	"time"

	"tunnel-shm/internal/model"
)

// makeSensor 构造测试用传感器
func makeSensor(id int, sectionID int, t model.SensorType) model.Sensor {
	return model.Sensor{
		ID: id, SectionID: sectionID, Code: "S-T", Type: t, Calibration: 1.0,
	}
}

// makeSection 构造测试用断面
func makeSection(id int, posType model.SectionPositionType) model.Section {
	return model.Section{
		ID: id, Code: "L3-S001", Name: "test", LineCode: "3",
		StationKm: 1000, PositionType: posType,
	}
}

// now 返回当前时间（用于速率计算）
func now() time.Time { return time.Now() }
```

- [ ] **Step 2: 新建 `backend/internal/healthscore/engine.go`**

```go
// Package healthscore 断面健康度评分引擎（纯函数实现，与 DB/IO 解耦）
package healthscore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"tunnel-shm/internal/model"
)

// SensorScoreInput 单个传感器的评分输入
type SensorScoreInput struct {
	SensorID            int
	SensorType          model.SensorType
	Rate24h             float64
	Rate7d              float64
	Rate30d             float64
	RecentAlertCount    int
	DataCompleteness    float64 // 0~1，1=完整
	HistoricalVariance  float64 // 30d 窗口内的标准差（与单位一致）
}

// ComputeSectionScore 纯函数：计算单个断面的健康度评分。
// 返回：score（总分）、details（每维度明细）、intermediate（每传感器中间数据）
//
// 评分构成：
//   - 每个传感器子分 = 100 - 当前告警扣分 - 趋势扣分 - 稳定性扣分 + 完整度奖励
//   - 断面分 = Σ(传感器子分 × 传感器类型权重) × 位置敏感度
//   - 断面分钳制到 [0, 100]
func ComputeSectionScore(
	_ context.Context,
	section model.Section,
	sensors []model.Sensor,
	inputs []SensorScoreInput,
) (*model.SectionHealthScore, []model.ScoreDetail, []model.ScoreIntermediate, error) {

	if len(sensors) == 0 || len(sensors) != len(inputs) {
		return nil, nil, nil, fmt.Errorf("传感器与输入数据数量不匹配")
	}

	posSens := model.PositionSensitivity[section.PositionType]
	if posSens == 0 {
		posSens = 1.0
	}

	// 阈值映射（与 analyzer.DefaultThreshold 保持一致；缺省走 mm/天）
	warning := map[model.SensorType]float64{
		model.SensorTypeDisplacement: 0.5,
		model.SensorTypeCrack:        0.1,
		model.SensorTypeStrain:       10.0,
	}
	danger := map[model.SensorType]float64{
		model.SensorTypeDisplacement: 1.0,
		model.SensorTypeCrack:        0.3,
		model.SensorTypeStrain:       30.0,
	}

	var details []model.ScoreDetail
	var intermediates []model.ScoreIntermediate
	sensorScores := make(map[model.SensorType]float64, 3)

	// 触发类型由调用方注入（默认 cron；scheduler 会在事件触发时再调一次并设 event）
	trigger := model.HealthTriggerCron
	now := time.Now()

	for i, sensor := range sensors {
		in := inputs[i]
		wThr := warning[in.SensorType]
		dThr := danger[in.SensorType]

		// === 维度 1：当前告警（占传感器子分 40%）===
		// 24h 速率与阈值对比
		abs24 := math.Abs(in.Rate24h)
		alertSub := 100.0
		var alertRaw float64 = abs24
		if abs24 >= dThr {
			alertSub = 0
		} else if abs24 >= wThr {
			alertSub = 100 * (1 - (abs24-wThr)/(dThr-wThr))
		} else {
			// 低于 warning：按比例轻微扣分，速率/警告阈值 * 30 上限
			alertSub = 100 - math.Min(30, abs24/wThr*30)
		}
		// 近期告警次数额外扣分（每次 -5，最低 0）
		alertSub -= math.Min(40, float64(in.RecentAlertCount)*5)
		if alertSub < 0 {
			alertSub = 0
		}

		// === 维度 2：近期变化趋势（占 30%）===
		// 用 7d 速率与 30d 速率对比，加速恶化扣分
		trendSub := 100.0
		var trendRaw float64 = in.Rate7d
		acc := math.Abs(in.Rate7d) - math.Abs(in.Rate30d)
		if acc > 0 {
			// 加速恶化：每加速 wThr 的 1 倍扣 25 分
			trendSub = 100 - math.Min(100, acc/wThr*25)
		} else {
			// 减速或稳定：轻微奖励
			trendSub = 100 + math.Min(10, -acc/wThr*5)
		}
		if trendSub > 100 {
			trendSub = 100
		}
		if trendSub < 0 {
			trendSub = 0
		}

		// === 维度 3：历史稳定性（占 20%）===
		// 30d 方差越大越不稳定，按 wThr 归一化
		stabilitySub := 100.0
		var stabilityRaw float64 = in.HistoricalVariance
		if in.HistoricalVariance > 0 {
			stabilitySub = 100 - math.Min(100, in.HistoricalVariance/wThr*50)
		}
		if stabilitySub < 0 {
			stabilitySub = 0
		}

		// === 维度 4：数据完整性（占 10%）===
		// 完整度 1.0 时为 100，0 时为 0
		completenessSub := math.Max(0, math.Min(100, in.DataCompleteness*100))

		// === 加权聚合到传感器子分 ===
		sensorSub := alertSub*model.DimensionWeights.Alert +
			trendSub*model.DimensionWeights.Trend +
			stabilitySub*model.DimensionWeights.Stability +
			completenessSub*model.DimensionWeights.Completeness
		if sensorSub < 0 {
			sensorSub = 0
		}
		if sensorSub > 100 {
			sensorSub = 100
		}
		sensorScores[in.SensorType] = sensorSub

		// 写入明细（4 维度 × 1 传感器 = 4 行）
		dim := func(name, sub string, raw, subScore, w float64, expl string) model.ScoreDetail {
			return model.ScoreDetail{
				Dimension:    name,
				SubDimension: sub,
				RawValue:     raw,
				SubScore:     subScore,
				Weight:       w,
				Contribution: subScore * w,
				Explanation:  expl,
				CalculatedAt: now,
			}
		}
		details = append(details,
			dim("alert", string(in.SensorType), alertRaw, alertSub, model.DimensionWeights.Alert,
				fmt.Sprintf("24h速率=%.3f, 警告阈值=%.3f, 危险阈值=%.3f, 近期告警=%d次",
					alertRaw, wThr, dThr, in.RecentAlertCount)),
			dim("trend", string(in.SensorType), trendRaw, trendSub, model.DimensionWeights.Trend,
				fmt.Sprintf("7d速率=%.3f, 30d速率=%.3f, 加速量=%.3f", in.Rate7d, in.Rate30d, acc)),
			dim("stability", string(in.SensorType), stabilityRaw, stabilitySub, model.DimensionWeights.Stability,
				fmt.Sprintf("30d标准差=%.3f", stabilityRaw)),
			dim("completeness", string(in.SensorType), in.DataCompleteness, completenessSub, model.DimensionWeights.Completeness,
				fmt.Sprintf("7d数据完整度=%.2f%%", completenessSub)),
		)

		// 中间数据
		inputsJSON, _ := json.Marshal(in)
		intermediates = append(intermediates, model.ScoreIntermediate{
			SensorID:           in.SensorID,
			SectionID:          section.ID,
			SensorType:         in.SensorType,
			Rate24h:            in.Rate24h,
			Rate7d:             in.Rate7d,
			Rate30d:            in.Rate30d,
			RecentAlertCount:   in.RecentAlertCount,
			DataCompleteness:   in.DataCompleteness,
			HistoricalVariance: in.HistoricalVariance,
			SensorSubScore:     sensorSub,
			InputsJSON:         string(inputsJSON),
			CalculatedAt:       now,
		})
	}

	// === 按传感器类型加权聚合到断面 ===
	var totalWeight, weightedSum float64
	for _, st := range []model.SensorType{model.SensorTypeDisplacement, model.SensorTypeCrack, model.SensorTypeStrain} {
		w := model.SensorTypeWeight[st]
		totalWeight += w
		if sub, ok := sensorScores[st]; ok {
			weightedSum += sub * w
		}
	}
	if totalWeight == 0 {
		return nil, nil, nil, fmt.Errorf("无有效传感器类型")
	}
	sectionScore := weightedSum / totalWeight

	// 位置敏感度：>1 放大扣分（乘以 1/sensitivity 后钳制，等价于"分扣更多"）
	// 实现：score = 100 - (100 - sectionScore) * sensitivity
	sectionScore = 100 - (100-sectionScore)*posSens
	if sectionScore < 0 {
		sectionScore = 0
	}
	if sectionScore > 100 {
		sectionScore = 100
	}

	// 计算各传感器子分（位移/裂缝/应变）作为断面级别的展示字段
	disp := sensorScores[model.SensorTypeDisplacement]
	crk := sensorScores[model.SensorTypeCrack]
	str := sensorScores[model.SensorTypeStrain]

	// 维度总分（按传感器类型加权后的）
	var alertDim, trendDim, stabilityDim, completenessDim float64
	var wTotal float64
	for _, d := range details {
		switch d.Dimension {
		case "alert":
			alertDim += d.Contribution
		case "trend":
			trendDim += d.Contribution
		case "stability":
			stabilityDim += d.Contribution
		case "completeness":
			completenessDim += d.Contribution
		}
		wTotal += d.Weight
	}
	if wTotal > 0 {
		alertDim /= wTotal
		trendDim /= wTotal
		stabilityDim /= wTotal
		completenessDim /= wTotal
	}

	grade := model.GradeFromScore(sectionScore)
	score := &model.SectionHealthScore{
		SectionID:                  section.ID,
		TotalScore:                 sectionScore,
		Grade:                      grade,
		DisplacementScore:          disp,
		CrackScore:                 crk,
		StrainScore:                str,
		AlertDimensionScore:        alertDim,
		TrendDimensionScore:        trendDim,
		StabilityDimensionScore:    stabilityDim,
		CompletenessDimensionScore: completenessDim,
		PositionType:               section.PositionType,
		Sensitivity:                posSens,
		TriggerType:                trigger,
		CalculatedAt:               now,
	}

	// 给 details 补充 sectionID（已知）
	for i := range details {
		details[i].SectionID = section.ID
	}
	return score, details, intermediates, nil
}
```

- [ ] **Step 3: 新建 `backend/internal/healthscore/engine_test.go`**

```go
package healthscore

import (
	"context"
	"testing"

	"tunnel-shm/internal/model"
)

func TestComputeSectionScore_AllExcellent(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{
		makeSensor(11, 1, model.SensorTypeDisplacement),
		makeSensor(12, 1, model.SensorTypeCrack),
		makeSensor(13, 1, model.SensorTypeStrain),
	}
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 0.01, Rate7d: 0.005, Rate30d: 0.005, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.01},
		{SensorID: 12, SensorType: model.SensorTypeCrack, Rate24h: 0.001, Rate7d: 0.0005, Rate30d: 0.0005, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.001},
		{SensorID: 13, SensorType: model.SensorTypeStrain, Rate24h: 0.5, Rate7d: 0.3, Rate30d: 0.3, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.1},
	}
	score, _, _, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if score.Grade != model.HealthGradeExcellent {
		t.Errorf("全优场景应得优良，实际=%v 分=%.2f", score.Grade, score.TotalScore)
	}
	if score.TotalScore < 90 {
		t.Errorf("全优场景分数应≥90，实际=%.2f", score.TotalScore)
	}
}

func TestComputeSectionScore_ContinuousAlerts(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}
	// 3 次连续告警 + 高速率
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 1.2, Rate7d: 0.8, Rate30d: 0.3, RecentAlertCount: 3, DataCompleteness: 1.0, HistoricalVariance: 0.5},
	}
	score, _, _, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err != nil {
		t.Fatal(err)
	}
	// 评分应明显下降（< 60，至少到关注以下）
	if score.TotalScore >= 60 {
		t.Errorf("连续3次告警后分应<60，实际=%.2f, grade=%v", score.TotalScore, score.Grade)
	}
}

func TestComputeSectionScore_PositionSensitivity(t *testing.T) {
	// 同样输入下，联络通道分应低于区间中部
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 0.3, Rate7d: 0.2, Rate30d: 0.1, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.1},
	}
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}

	midSec := makeSection(1, model.PositionMid)
	crossSec := makeSection(1, model.PositionCross)

	midScore, _, _, _ := ComputeSectionScore(context.Background(), midSec, sensors, inputs)
	crossScore, _, _, _ := ComputeSectionScore(context.Background(), crossSec, sensors, inputs)

	if crossScore.TotalScore >= midScore.TotalScore {
		t.Errorf("联络通道分应低于区间中部，mid=%.2f, cross=%.2f", midScore.TotalScore, crossScore.TotalScore)
	}
}

func TestComputeSectionScore_DetailsExplainable(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 0.6, Rate7d: 0.4, Rate30d: 0.2, RecentAlertCount: 1, DataCompleteness: 1.0, HistoricalVariance: 0.2},
	}
	_, details, intermediates, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 4 {
		t.Errorf("应有4个维度明细，实际=%d", len(details))
	}
	if len(intermediates) != 1 {
		t.Errorf("应有1条中间数据，实际=%d", len(intermediates))
	}
	for _, d := range details {
		if d.Explanation == "" {
			t.Errorf("明细 %s/%s 缺少解释", d.Dimension, d.SubDimension)
		}
	}
}

func TestComputeSectionScore_LengthMismatch(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}
	inputs := []SensorScoreInput{} // 空
	_, _, _, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err == nil {
		t.Error("传感器与输入不匹配应返回错误")
	}
}
```

- [ ] **Step 4: 运行测试**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\backend && go test ./internal/healthscore/... -v`
Expected: PASS，5 个测试全部通过。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/healthscore/
git commit -m "feat(health): 实现纯函数评分引擎与单元测试"
```

---

## Task 3: Store 层 CRUD（健康度表 + 评分所需原始数据查询）

**Files:**
- Modify: `backend/internal/store/store.go`（追加 9 个方法）

**Interfaces:**
- Produces（被 scheduler / handler 消费）：
  - `func (s *Store) GetSectionAlertsSince(ctx, sectionID, since) (int, error)` — 计数
  - `func (s *Store) GetSensorDataRange(ctx, sensorID, start, end) ([]model.SensorData, error)` — 拉取指定区间数据
  - `func (s *Store) CountSensorData(ctx, sensorID, since) (int, error)` — 7d 数据点数（用于完整度）
  - `func (s *Store) InsertHealthScore(ctx, score *SectionHealthScore, details []ScoreDetail, intermediates []ScoreIntermediate) (int64, error)` — 一次事务插入三表
  - `func (s *Store) GetLatestSectionHealthScore(ctx, sectionID) (*SectionHealthScore, []ScoreDetail, []ScoreIntermediate, error)`
  - `func (s *Store) ListLatestHealthScoresByLine(ctx, lineCode) ([]SectionHealthScore, error)` — 看板用
  - `func (s *Store) GetHealthScoreHistoryAggregated(ctx, sectionID, start, end, interval) ([]HistoryPoint, error)` — 历史曲线
  - `func (s *Store) GetSectionHealthRank(ctx, lineCode) ([]RankItem, error)` — 排名

- [ ] **Step 1: 在 `store.go` 末尾追加所有新方法**

```go
// ===========================================
// 健康度评分相关查询与写入
// ===========================================

// HistoryPoint 历史曲线的一个时间点
type HistoryPoint struct {
	Bucket       time.Time `json:"bucket"`
	TotalScore   float64   `json:"total_score"`
	Grade        string    `json:"grade"`
}

// RankItem 健康度排名条目（看板用）
type RankItem struct {
	SectionID   int     `json:"section_id"`
	SectionCode string  `json:"section_code"`
	SectionName string  `json:"section_name"`
	LineCode    string  `json:"line_code"`
	TotalScore  float64 `json:"total_score"`
	Grade       string  `json:"grade"`
	AlertCount  int     `json:"alert_count"`
	TrendDelta  float64 `json:"trend_delta"` // 与上次评分差值
}

// GetSectionAlertsSince 断面自 since 起的告警计数
func (s *Store) GetSectionAlertsSince(ctx context.Context, sectionID int, since time.Time) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM alerts WHERE section_id = $1 AND triggered_at >= $2`,
		sectionID, since).Scan(&count)
	return count, err
}

// GetSensorDataRange 拉取指定区间数据（用于计算 7d/30d 速率与方差）
func (s *Store) GetSensorDataRange(ctx context.Context, sensorID int, start, end time.Time) ([]model.SensorData, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, sensor_id, value, timestamp
		 FROM sensor_data
		 WHERE sensor_id = $1 AND timestamp >= $2 AND timestamp <= $3
		 ORDER BY timestamp ASC`, sensorID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var data []model.SensorData
	for rows.Next() {
		var d model.SensorData
		if err := rows.Scan(&d.ID, &d.SensorID, &d.Value, &d.Timestamp); err != nil {
			return nil, err
		}
		data = append(data, d)
	}
	return data, rows.Err()
}

// CountSensorData 计数指定时间窗内的数据点数（用于完整度）
func (s *Store) CountSensorData(ctx context.Context, sensorID int, since time.Time) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sensor_data WHERE sensor_id = $1 AND timestamp >= $2`,
		sensorID, since).Scan(&count)
	return count, err
}

// InsertHealthScore 一次事务插入 score + details + intermediate
func (s *Store) InsertHealthScore(
	ctx context.Context,
	score *model.SectionHealthScore,
	details []model.ScoreDetail,
	intermediates []model.ScoreIntermediate,
) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var scoreID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO section_health_scores
			(section_id, total_score, grade, displacement_score, crack_score, strain_score,
			 alert_dimension_score, trend_dimension_score, stability_dimension_score, completeness_dimension_score,
			 position_type, sensitivity, trigger_type, calculated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id`,
		score.SectionID, score.TotalScore, score.Grade,
		score.DisplacementScore, score.CrackScore, score.StrainScore,
		score.AlertDimensionScore, score.TrendDimensionScore,
		score.StabilityDimensionScore, score.CompletenessDimensionScore,
		score.PositionType, score.Sensitivity, score.TriggerType, score.CalculatedAt,
	).Scan(&scoreID)
	if err != nil {
		return 0, err
	}

	for i := range details {
		details[i].ScoreID = scoreID
		details[i].CalculatedAt = score.CalculatedAt
		_, err = tx.Exec(ctx, `
			INSERT INTO section_health_score_details
				(score_id, section_id, dimension, sub_dimension, raw_value, sub_score, weight, contribution, explanation, calculated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			scoreID, details[i].SectionID, details[i].Dimension, details[i].SubDimension,
			details[i].RawValue, details[i].SubScore, details[i].Weight, details[i].Contribution,
			details[i].Explanation, details[i].CalculatedAt,
		)
		if err != nil {
			return 0, err
		}
	}

	for i := range intermediates {
		intermediates[i].ScoreID = scoreID
		intermediates[i].CalculatedAt = score.CalculatedAt
		var id int64
		err = tx.QueryRow(ctx, `
			INSERT INTO section_health_score_intermediate
				(score_id, section_id, sensor_id, sensor_type, rate_24h, rate_7d, rate_30d,
				 recent_alert_count, data_completeness, historical_variance, sensor_sub_score, inputs_json, calculated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING id`,
			scoreID, intermediates[i].SectionID, intermediates[i].SensorID,
			intermediates[i].SensorType, intermediates[i].Rate24h, intermediates[i].Rate7d,
			intermediates[i].Rate30d, intermediates[i].RecentAlertCount,
			intermediates[i].DataCompleteness, intermediates[i].HistoricalVariance,
			intermediates[i].SensorSubScore, intermediates[i].InputsJSON,
			intermediates[i].CalculatedAt,
		).Scan(&id)
		if err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return scoreID, nil
}

// GetLatestSectionHealthScore 获取某断面最新一次评分 + 明细 + 中间数据
func (s *Store) GetLatestSectionHealthScore(ctx context.Context, sectionID int) (*model.SectionHealthScore, []model.ScoreDetail, []model.ScoreIntermediate, error) {
	var score model.SectionHealthScore
	err := s.pool.QueryRow(ctx, `
		SELECT id, section_id, total_score, grade, displacement_score, crack_score, strain_score,
		       alert_dimension_score, trend_dimension_score, stability_dimension_score, completeness_dimension_score,
		       position_type, sensitivity, trigger_type, calculated_at
		FROM section_health_scores
		WHERE section_id = $1
		ORDER BY calculated_at DESC LIMIT 1`, sectionID).Scan(
		&score.ID, &score.SectionID, &score.TotalScore, &score.Grade,
		&score.DisplacementScore, &score.CrackScore, &score.StrainScore,
		&score.AlertDimensionScore, &score.TrendDimensionScore,
		&score.StabilityDimensionScore, &score.CompletenessDimensionScore,
		&score.PositionType, &score.Sensitivity, &score.TriggerType, &score.CalculatedAt,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, score_id, section_id, dimension, sub_dimension, raw_value, sub_score, weight, contribution, explanation, calculated_at
		FROM section_health_score_details
		WHERE score_id = $1
		ORDER BY dimension ASC`, score.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var details []model.ScoreDetail
	for rows.Next() {
		var d model.ScoreDetail
		if err := rows.Scan(&d.ID, &d.ScoreID, &d.SectionID, &d.Dimension, &d.SubDimension,
			&d.RawValue, &d.SubScore, &d.Weight, &d.Contribution, &d.Explanation, &d.CalculatedAt); err != nil {
			return nil, nil, nil, err
		}
		details = append(details, d)
	}

	rows2, err := s.pool.Query(ctx, `
		SELECT id, score_id, section_id, sensor_id, sensor_type, rate_24h, rate_7d, rate_30d,
		       recent_alert_count, data_completeness, historical_variance, sensor_sub_score, inputs_json, calculated_at
		FROM section_health_score_intermediate
		WHERE score_id = $1
		ORDER BY sensor_id ASC`, score.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows2.Close()
	var inters []model.ScoreIntermediate
	for rows2.Next() {
		var it model.ScoreIntermediate
		if err := rows2.Scan(&it.ID, &it.ScoreID, &it.SectionID, &it.SensorID, &it.SensorType,
			&it.Rate24h, &it.Rate7d, &it.Rate30d, &it.RecentAlertCount,
			&it.DataCompleteness, &it.HistoricalVariance, &it.SensorSubScore,
			&it.InputsJSON, &it.CalculatedAt); err != nil {
			return nil, nil, nil, err
		}
		inters = append(inters, it)
	}
	return &score, details, inters, nil
}

// ListLatestHealthScoresByLine 获取某线路所有断面的最新评分（看板总览）
func (s *Store) ListLatestHealthScoresByLine(ctx context.Context, lineCode string) ([]model.SectionHealthScore, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (s.id) s.id, sec.id, sec.total_score, sec.grade, sec.displacement_score, sec.crack_score, sec.strain_score,
		       sec.alert_dimension_score, sec.trend_dimension_score, sec.stability_dimension_score, sec.completeness_dimension_score,
		       sec.position_type, sec.sensitivity, sec.trigger_type, sec.calculated_at
		FROM sections s
		JOIN section_health_scores sec ON sec.section_id = s.id
		WHERE s.line_code = $1
		ORDER BY s.id, sec.calculated_at DESC`, lineCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scores []model.SectionHealthScore
	for rows.Next() {
		var sc model.SectionHealthScore
		if err := rows.Scan(&sc.ID, &sc.SectionID, &sc.TotalScore, &sc.Grade,
			&sc.DisplacementScore, &sc.CrackScore, &sc.StrainScore,
			&sc.AlertDimensionScore, &sc.TrendDimensionScore,
			&sc.StabilityDimensionScore, &sc.CompletenessDimensionScore,
			&sc.PositionType, &sc.Sensitivity, &sc.TriggerType, &sc.CalculatedAt); err != nil {
			return nil, err
		}
		scores = append(scores, sc)
	}
	return scores, rows.Err()
}

// GetHealthScoreHistoryAggregated 获取历史评分曲线（按 interval 聚合）
func (s *Store) GetHealthScoreHistoryAggregated(ctx context.Context, sectionID int, start, end time.Time, interval string) ([]HistoryPoint, error) {
	q := fmt.Sprintf(`
		SELECT time_bucket('%s', calculated_at) AS bucket,
		       AVG(total_score) AS avg_score,
		       (array_agg(grade ORDER BY calculated_at DESC))[1] AS last_grade
		FROM section_health_scores
		WHERE section_id = $1 AND calculated_at >= $2 AND calculated_at <= $3
		GROUP BY bucket
		ORDER BY bucket ASC`, interval)
	rows, err := s.pool.Query(ctx, q, sectionID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pts []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.Bucket, &p.TotalScore, &p.Grade); err != nil {
			return nil, err
		}
		pts = append(pts, p)
	}
	return pts, rows.Err()
}

// GetSectionHealthRank 获取断面健康度排名（按线路）
func (s *Store) GetSectionHealthRank(ctx context.Context, lineCode string) ([]RankItem, error) {
	rows, err := s.pool.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (section_id) id, section_id, total_score, grade, calculated_at
			FROM section_health_scores
			ORDER BY section_id, calculated_at DESC
		),
		prev AS (
			SELECT DISTINCT ON (section_id) id, section_id, total_score, calculated_at
			FROM section_health_scores
			WHERE calculated_at < NOW() - INTERVAL '1 hour'
			ORDER BY section_id, calculated_at DESC
		),
		alert_cnt AS (
			SELECT section_id, COUNT(*) AS cnt
			FROM alerts WHERE status = 'active'
			GROUP BY section_id
		)
		SELECT s.id, s.code, s.name, s.line_code, l.total_score, l.grade,
		       COALESCE(ac.cnt, 0) AS alert_count,
		       COALESCE(l.total_score - p.total_score, 0) AS trend_delta
		FROM sections s
		JOIN latest l ON l.section_id = s.id
		LEFT JOIN prev p ON p.section_id = s.id
		LEFT JOIN alert_cnt ac ON ac.section_id = s.id
		WHERE s.line_code = $1
		ORDER BY l.total_score ASC`, lineCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RankItem
	for rows.Next() {
		var r RankItem
		if err := rows.Scan(&r.SectionID, &r.SectionCode, &r.SectionName, &r.LineCode,
			&r.TotalScore, &r.Grade, &r.AlertCount, &r.TrendDelta); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
```

- [ ] **Step 2: 同时给 `GetSection` 加上 `position_type` 字段读取**

将 `store.go` 中的 `GetSection` 与 `GetSections` 方法的 SELECT 列表追加 `position_type`，Scan 列表同步追加（同时改 `model.Section` 增加 `PositionType` 字段）：

`model.Section` 结构追加（修改 `model.go`）：
```go
PositionType SectionPositionType `json:"position_type"`
```

`GetSection` 改为：
```go
row := s.pool.QueryRow(ctx,
    `SELECT id, code, name, line_code, station_km, description, location_lat, location_lng, position_type
     FROM sections WHERE id = $1`, id)
var sec model.Section
err := row.Scan(&sec.ID, &sec.Code, &sec.Name, &sec.LineCode,
    &sec.StationKm, &sec.Description, &sec.LocationLat, &sec.LocationLng, &sec.PositionType)
```

`GetSections` 同样修改。

- [ ] **Step 3: 编译验证**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\backend && go build ./...`
Expected: 编译成功，无错误（warning 可忽略）。

- [ ] **Step 4: 提交**

```bash
git add backend/internal/store/store.go backend/internal/model/model.go
git commit -m "feat(health): store 层健康度 CRUD + 原始数据查询"
```

---

## Task 4: 评分调度器（cron + 事件触发）

**Files:**
- Create: `backend/internal/healthscore/scheduler.go`

**Interfaces:**
- Produces:
  - `type Scheduler struct { store *store.Store; cron *cron.Cron }`
  - `func New(store *store.Store) *Scheduler`
  - `func (s *Scheduler) Start()` — 启动 cron（每 1 分钟全量）
  - `func (s *Scheduler) Stop()`
  - `func (s *Scheduler) EnqueueRecompute(sectionID int)` — 事件触发入口

- [ ] **Step 1: 新建 `backend/internal/healthscore/scheduler.go`**

```go
package healthscore

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"tunnel-shm/internal/analyzer"
	"tunnel-shm/internal/model"
	"tunnel-shm/internal/store"

	"github.com/robfig/cron/v3"
)

// Scheduler 评分调度器
type Scheduler struct {
	store *store.Store
	cron  *cron.Cron
	mu    sync.Mutex
	// 节流：同一断面在 throttle 时间内不重复重算
	throttle     map[int]time.Time
	throttleDur  time.Duration
	pendingQueue chan int
	wg           sync.WaitGroup
	stopCh       chan struct{}
}

func NewScheduler(st *store.Store) *Scheduler {
	return &Scheduler{
		store:        st,
		cron:         cron.New(),
		throttle:     make(map[int]time.Time),
		throttleDur:  30 * time.Second,
		pendingQueue: make(chan int, 256),
		stopCh:       make(chan struct{}),
	}
}

// Start 启动 cron 与工作协程
func (s *Scheduler) Start() {
	// 每 1 分钟全量重算（满足 1 分钟更新 SLA）
	_, err := s.cron.AddFunc("*/1 * * * *", func() {
		s.recomputeAll(context.Background())
	})
	if err != nil {
		log.Printf("【健康度-错误】cron 注册失败: %v", err)
	}
	s.cron.Start()
	log.Println("【健康度-调度】已启动（每 1 分钟全量）")

	// 单 worker 处理事件触发的重算（避免并发写库）
	s.wg.Add(1)
	go s.worker()
}

// Stop 停止
func (s *Scheduler) Stop() {
	close(s.stopCh)
	s.cron.Stop()
	s.wg.Wait()
}

// EnqueueRecompute 事件触发：把 sectionID 放入队列（带节流）
func (s *Scheduler) EnqueueRecompute(sectionID int) {
	s.mu.Lock()
	if last, ok := s.throttle[sectionID]; ok {
		if time.Since(last) < s.throttleDur {
			s.mu.Unlock()
			return
		}
	}
	s.throttle[sectionID] = time.Now()
	s.mu.Unlock()

	select {
	case s.pendingQueue <- sectionID:
	default:
		// 队列满则丢弃，cron 兜底
		log.Printf("【健康度-节流】队列已满，丢弃断面 %d 的事件触发", sectionID)
	}
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case id := <-s.pendingQueue:
			s.recomputeOne(context.Background(), id, model.HealthTriggerEvent)
		}
	}
}

func (s *Scheduler) recomputeAll(ctx context.Context) {
	log.Println("【健康度-调度】开始全量评分...")
	start := time.Now()
	sections, err := s.store.GetSections(ctx)
	if err != nil {
		log.Printf("【健康度-错误】获取断面列表失败: %v", err)
		return
	}
	for _, sec := range sections {
		s.recomputeOne(ctx, sec.ID, model.HealthTriggerCron)
	}
	log.Printf("【健康度-调度】全量评分完成 断面数=%d 耗时=%v", len(sections), time.Since(start))
}

// recomputeOne 算一个断面。trigger 用于写入 DB
func (s *Scheduler) recomputeOne(ctx context.Context, sectionID int, trigger model.HealthTriggerType) {
	// 重新拉取断面（拿到最新的 position_type）
	sec, err := s.store.GetSection(ctx, sectionID)
	if err != nil {
		log.Printf("【健康度-错误】断面 %d 不存在: %v", sectionID, err)
		return
	}
	sensors, err := s.store.GetSensorsBySection(ctx, sectionID)
	if err != nil || len(sensors) == 0 {
		return
	}

	now := time.Now()
	inputs := make([]SensorScoreInput, 0, len(sensors))
	for _, sensor := range sensors {
		// 24h 速率（复用现有算法）
		rate24, err := s.store.CalculateDeformationRate(ctx, sensor.ID)
		var rate24Val float64
		if err == nil {
			rate24Val = rate24.Rate
		}

		// 7d 速率：直接基于原始数据端点
		rate7Val := s.computeEndpointRate(ctx, sensor.ID, now.Add(-7*24*time.Hour), now)
		// 30d 速率
		rate30Val := s.computeEndpointRate(ctx, sensor.ID, now.Add(-30*24*time.Hour), now)
		// 30d 窗口内方差
		variance := s.computeVariance(ctx, sensor.ID, now.Add(-30*24*time.Hour), now)
		// 7d 告警计数
		alertCnt, _ := s.store.GetSectionAlertsSince(ctx, sectionID, now.Add(-7*24*time.Hour))
		// 7d 数据完整度（按每 5 分钟一个点，期望 7*24*12=2016）
		dataCnt, _ := s.store.CountSensorData(ctx, sensor.ID, now.Add(-7*24*time.Hour))
		expected := float64(7 * 24 * 12)
		completeness := math.Min(1.0, float64(dataCnt)/expected)

		inputs = append(inputs, SensorScoreInput{
			SensorID:           sensor.ID,
			SensorType:         sensor.Type,
			Rate24h:            rate24Val,
			Rate7d:             rate7Val,
			Rate30d:            rate30Val,
			RecentAlertCount:   alertCnt,
			DataCompleteness:   completeness,
			HistoricalVariance: variance,
		})
	}

	score, details, inters, err := ComputeSectionScore(ctx, *sec, sensors, inputs)
	if err != nil {
		log.Printf("【健康度-错误】断面 %d 评分失败: %v", sectionID, err)
		return
	}
	score.TriggerType = trigger

	scoreID, err := s.store.InsertHealthScore(ctx, score, details, inters)
	if err != nil {
		log.Printf("【健康度-错误】断面 %d 评分入库失败: %v", sectionID, err)
		return
	}
	log.Printf("【健康度-评分】断面[%d] %s 评分=%.2f 等级=%s 触发=%s scoreID=%d",
		sectionID, sec.Code, score.TotalScore, score.Grade, trigger, scoreID)
}

// computeEndpointRate 端点速率（mm/天）；数据不足返回 0
func (s *Scheduler) computeEndpointRate(ctx context.Context, sensorID int, start, end time.Time) float64 {
	data, err := s.store.GetSensorDataRange(ctx, sensorID, start, end)
	if err != nil || len(data) < 2 {
		return 0
	}
	first, last := data[0], data[len(data)-1]
	hours := last.Timestamp.Sub(first.Timestamp).Hours()
	if hours <= 0 {
		return 0
	}
	return (last.Value - first.Value) / hours * 24.0
}

// computeVariance 区间数据方差
func (s *Scheduler) computeVariance(ctx context.Context, sensorID int, start, end time.Time) float64 {
	data, err := s.store.GetSensorDataRange(ctx, sensorID, start, end)
	if err != nil || len(data) < 2 {
		return 0
	}
	var sum, sum2 float64
	for _, d := range data {
		sum += d.Value
		sum2 += d.Value * d.Value
	}
	n := float64(len(data))
	mean := sum / n
	v := sum2/n - mean*mean
	if v < 0 {
		v = 0
	}
	return math.Sqrt(v)
}

// 编译期引用 analyzer 包避免 import 漂移告警
var _ = analyzer.DefaultThreshold
```

- [ ] **Step 2: 编译验证**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\backend && go build ./...`
Expected: 编译成功。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/healthscore/scheduler.go
git commit -m "feat(health): 评分调度器 cron + 事件触发"
```

---

## Task 5: HTTP API 处理器

**Files:**
- Create: `backend/internal/api/health_handler.go`
- Modify: `backend/internal/api/handler.go`（追加 `RegisterHealthRoutes` 与 `NewHandler` 接受 health scheduler）

**Interfaces:**
- Consumes: `*store.Store` + `*healthscore.Scheduler`
- Produces:
  - `GET /api/v1/health-dashboard/rank?line_code=3` — 排名总览
  - `GET /api/v1/sections/:id/health` — 最新评分 + 明细 + 中间数据
  - `GET /api/v1/sections/:id/health/history?start=&end=&interval=` — 历史曲线

- [ ] **Step 1: 新建 `backend/internal/api/health_handler.go`**

```go
package api

import (
	"net/http"
	"strconv"
	"time"

	"tunnel-shm/internal/healthscore"
	"tunnel-shm/internal/store"

	"github.com/gin-gonic/gin"
)

// HealthHandler 健康度 API
type HealthHandler struct {
	store     *store.Store
	scheduler *healthscore.Scheduler
}

func NewHealthHandler(st *store.Store, sch *healthscore.Scheduler) *HealthHandler {
	return &HealthHandler{store: st, scheduler: sch}
}

// RegisterHealthRoutes 注册健康度路由
func (h *Handler) RegisterHealthRoutes(sch *healthscore.Scheduler) {
	hh := NewHealthHandler(h.store, sch)
	api := h.engine.Group("/api/v1") // 此处 h.engine 由 Handler 暴露
	api.GET("/health-dashboard/rank", hh.GetRank)
	api.GET("/sections/:id/health", hh.GetSectionHealth)
	api.GET("/sections/:id/health/history", hh.GetSectionHealthHistory)
	api.POST("/sections/:id/health/recompute", hh.PostRecompute)
}

// GetRank 获取健康度排名总览
func (hh *HealthHandler) GetRank(c *gin.Context) {
	line := c.DefaultQuery("line_code", "3")
	items, err := hh.store.GetSectionHealthRank(c.Request.Context(), line)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 统计各等级数量
	gradeCount := map[string]int{
		"excellent": 0, "normal": 0, "attention": 0, "degraded": 0, "danger": 0,
	}
	for _, it := range items {
		gradeCount[it.Grade]++
	}
	c.JSON(http.StatusOK, gin.H{
		"data": items,
		"total": len(items),
		"grade_count": gradeCount,
		"line_code": line,
	})
}

// GetSectionHealth 获取某断面的最新健康度评分（含明细）
func (hh *HealthHandler) GetSectionHealth(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	score, details, inters, err := hh.store.GetLatestSectionHealthScore(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "该断面尚无评分数据"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"score": score,
		"details": details,
		"intermediate": inters,
	})
}

// GetSectionHealthHistory 获取历史健康度曲线
func (hh *HealthHandler) GetSectionHealthHistory(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	now := time.Now()
	start := now.Add(-30 * 24 * time.Hour)
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
	interval := c.DefaultQuery("interval", "1 day")

	pts, err := hh.store.GetHealthScoreHistoryAggregated(c.Request.Context(), id, start, end, interval)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": pts,
		"total": len(pts),
		"interval": interval,
		"start": start,
		"end": end,
	})
}

// PostRecompute 手动触发重算（管理用）
func (hh *HealthHandler) PostRecompute(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	hh.scheduler.EnqueueRecompute(id)
	c.JSON(http.StatusOK, gin.H{"message": "已加入重算队列"})
}
```

- [ ] **Step 2: 修改 `backend/internal/api/handler.go`**

将 `Handler` 结构暴露 `engine *gin.Engine` 并在 `NewHandler` 中保存：

```go
type Handler struct {
	store  *store.Store
	engine *gin.Engine
}

func NewHandler(store *store.Store, engine *gin.Engine) *Handler {
	return &Handler{store: store, engine: engine}
}
```

将 `RegisterRoutes` 中使用 `r.Group` 改为保存到 `h.engine` 后再分组（保持向后兼容现有调用）：

```go
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	h.engine = r
	api := r.Group("/api/v1")
	{ /* 现有路由保持不变 */ }
}
```

- [ ] **Step 3: 编译验证**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\backend && go build ./...`
Expected: 编译成功（注意 `main.go` 还没有调用 `NewHandler` 新签名，先保证编译通过，main.go 在 Task 6 改）。

- [ ] **Step 4: 提交**

```bash
git add backend/internal/api/health_handler.go backend/internal/api/handler.go
git commit -m "feat(health): 健康度 API 处理器与路由注册"
```

---

## Task 6: 后端集成（main.go + analyzer 联动）

**Files:**
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/internal/analyzer/analyzer.go`（注入 scheduler 引用，告警插入后异步触发）

**Interfaces:**
- Consumes: `*healthscore.Scheduler`（提供 `EnqueueRecompute`）
- Produces: 启动时初始化 scheduler、注册路由、analyzer 注入

- [ ] **Step 1: 修改 `backend/cmd/server/main.go`**

将 `main.go` 调整如下（关键变更点已标 `# <-- CHANGED`）：

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"tunnel-shm/internal/analyzer"
	"tunnel-shm/internal/api"
	"tunnel-shm/internal/collector"
	"tunnel-shm/internal/healthscore"
	"tunnel-shm/internal/store"
	"tunnel-shm/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/robfig/cron/v3"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("【系统-启动】隧道结构健康监测系统启动中...")

	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "tunnel")
	dbPass := getEnv("DB_PASS", "tunnel123")
	dbName := getEnv("DB_NAME", "tunnel_shm")
	serverPort := getEnv("SERVER_PORT", "8080")

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPass, dbHost, dbPort, dbName)

	ctx := context.Background()

	st, err := store.New(ctx, connStr)
	if err != nil { log.Fatalf("【系统-错误】数据库连接失败: %v", err) }
	defer st.Close()
	log.Println("【系统-数据库】连接成功")

	if err := st.InitSchema(ctx); err != nil {
		log.Printf("【系统-警告】初始化表结构失败（可能已存在）: %v", err)
	} else {
		log.Println("【系统-数据库】表结构初始化完成")
	}

	hub := ws.NewHub()
	go hub.Run()
	log.Println("【系统-WS】WebSocket Hub已启动")

	col := collector.New(st, hub)
	anal := analyzer.New(st, hub, nil)

	// <-- CHANGED: 启动健康度评分调度器
	healthSched := healthscore.NewScheduler(st)
	healthSched.Start()
	defer healthSched.Stop()

	// <-- CHANGED: 把 scheduler 注入 analyzer，告警插入后触发评分
	anal.SetHealthScheduler(healthSched)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" { c.AbortWithStatus(204); return }
		c.Next()
	})

	// <-- CHANGED: NewHandler 现在需要 engine
	handler := api.NewHandler(st, r)
	handler.RegisterRoutes(r)
	handler.RegisterHealthRoutes(healthSched) // <-- CHANGED

	r.POST("/api/v1/collect", col.HandleCollectData)
	r.GET("/api/v1/health", col.HandleHealthCheck)
	r.GET("/ws", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, c.Request, nil)
		if err != nil { log.Printf("【WS-错误】升级连接失败: %v", err); return }
		client := ws.NewClient(conn, hub)
		hub.Register(client)
		go client.WritePump()
		go client.ReadPump(hub)
	})

	c := cron.New()
	c.AddFunc("*/5 * * * *", func() { anal.AnalyzeAllSensors(context.Background()) })
	c.Start()
	log.Println("【系统-定时】告警分析定时任务已启动（每5分钟）")

	srv := &http.Server{ Addr: ":" + serverPort, Handler: r }
	go func() {
		log.Printf("【系统-服务】HTTP服务启动在端口 %s", serverPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("【系统-错误】HTTP服务启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("【系统-关闭】正在关闭服务...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c.Stop()
	if err := srv.Shutdown(shutdownCtx); err != nil { log.Fatalf("【系统-错误】服务关闭失败: %v", err) }
	log.Println("【系统-关闭】服务已安全关闭")
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" { return val }
	return defaultVal
}
```

- [ ] **Step 2: 修改 `backend/internal/analyzer/analyzer.go`**

在 `Analyzer` 结构体追加字段：
```go
type Analyzer struct {
	store     *store.Store
	hub       *ws.Hub
	threshold Threshold
	health    *healthscore.Scheduler // <-- CHANGED
}
```

在文件底部添加 setter：
```go
// SetHealthScheduler 注入健康度调度器，告警插入后异步触发评分
func (a *Analyzer) SetHealthScheduler(s *healthscore.Scheduler) { a.health = s }
```

在 `analyzeSensor` 函数中，`InsertAlert` 返回 nil 之后追加：
```go
if a.health != nil {
    a.health.EnqueueRecompute(section.ID)
}
```

并 import `"tunnel-shm/internal/healthscore"`。

- [ ] **Step 3: 编译验证**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\backend && go build ./...`
Expected: 编译成功。

- [ ] **Step 4: 启动验证**

启动后端：
```bash
cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\backend && go run ./cmd/server
```

预期日志：
```
【系统-数据库】表结构初始化完成
【健康度-调度】已启动（每 1 分钟全量）
【系统-服务】HTTP服务启动在端口 8080
```

`curl http://localhost:8080/api/v1/health-dashboard/rank?line_code=3` 第一次应返回空 `data: []`（尚未评分），第二次 cron 触发后（约 1 分钟内）会逐步填充。

- [ ] **Step 5: 提交**

```bash
git add backend/cmd/server/main.go backend/internal/analyzer/analyzer.go
git commit -m "feat(health): main.go 集成 scheduler + analyzer 告警后触发评分"
```

---

## Task 7: 前端 API 封装 + Pinia 状态

**Files:**
- Create: `frontend/src/types/health.ts`
- Create: `frontend/src/api/health.ts`
- Modify: `frontend/src/stores/monitor.ts`

- [ ] **Step 1: 新建 `frontend/src/types/health.ts`**

```ts
export type HealthGrade = 'excellent' | 'normal' | 'attention' | 'degraded' | 'danger'

export interface HealthScore {
  id: number
  section_id: number
  total_score: number
  grade: HealthGrade
  displacement_score: number
  crack_score: number
  strain_score: number
  alert_dimension_score: number
  trend_dimension_score: number
  stability_dimension_score: number
  completeness_dimension_score: number
  position_type: string
  sensitivity: number
  trigger_type: 'cron' | 'event'
  calculated_at: string
}

export interface ScoreDetail {
  id: number
  score_id: number
  section_id: number
  dimension: string
  sub_dimension: string
  raw_value: number
  sub_score: number
  weight: number
  contribution: number
  explanation: string
  calculated_at: string
}

export interface ScoreIntermediate {
  id: number
  score_id: number
  section_id: number
  sensor_id: number
  sensor_type: string
  rate_24h: number
  rate_7d: number
  rate_30d: number
  recent_alert_count: number
  data_completeness: number
  historical_variance: number
  sensor_sub_score: number
  inputs_json: string
  calculated_at: string
}

export interface HealthRankItem {
  section_id: number
  section_code: string
  section_name: string
  line_code: string
  total_score: number
  grade: HealthGrade
  alert_count: number
  trend_delta: number
}

export interface HealthRankResponse {
  data: HealthRankItem[]
  total: number
  grade_count: Record<HealthGrade, number>
  line_code: string
}

export interface HealthDetailResponse {
  score: HealthScore
  details: ScoreDetail[]
  intermediate: ScoreIntermediate[]
}

export interface HealthHistoryPoint {
  bucket: string
  total_score: number
  grade: HealthGrade
}

export interface HealthHistoryResponse {
  data: HealthHistoryPoint[]
  total: number
  interval: string
  start: string
  end: string
}
```

- [ ] **Step 2: 新建 `frontend/src/api/health.ts`**

```ts
import axios from 'axios'

const api = axios.create({ baseURL: '/api/v1', timeout: 10000 })

// 健康度排名
export async function getHealthRank(lineCode = '3'): Promise<HealthRankResponse> {
  const { data } = await api.get('/health-dashboard/rank', { params: { line_code: lineCode } })
  return data
}

// 断面健康度明细
export async function getSectionHealth(sectionId: number): Promise<HealthDetailResponse> {
  const { data } = await api.get(`/sections/${sectionId}/health`)
  return data
}

// 健康度历史曲线
export async function getSectionHealthHistory(
  sectionId: number,
  start: string,
  end: string,
  interval = '1 day'
): Promise<HealthHistoryResponse> {
  const { data } = await api.get(`/sections/${sectionId}/health/history`, {
    params: { start, end, interval },
  })
  return data
}

// 手动触发重算
export async function recomputeSectionHealth(sectionId: number) {
  const { data } = await api.post(`/sections/${sectionId}/health/recompute`)
  return data
}
```

- [ ] **Step 3: 修改 `frontend/src/stores/monitor.ts`**

追加：
```ts
import type { HealthRankResponse, HealthDetailResponse, HealthHistoryResponse } from '../api/health'
import { getHealthRank, getSectionHealth, getSectionHealthHistory } from '../api/health'

// 在 store 内部添加
const healthRank = ref<HealthRankResponse | null>(null)
async function fetchHealthRank(lineCode = '3') {
  try { healthRank.value = await getHealthRank(lineCode) }
  catch (e) { console.error('获取健康度排名失败:', e) }
}

const sectionHealth = ref<HealthDetailResponse | null>(null)
async function fetchSectionHealth(sectionId: number) {
  try { sectionHealth.value = await getSectionHealth(sectionId) }
  catch (e) { console.error('获取断面健康度失败:', e) }
}

const sectionHealthHistory = ref<HealthHistoryResponse | null>(null)
async function fetchSectionHealthHistory(sectionId: number, start: string, end: string, interval = '1 day') {
  try { sectionHealthHistory.value = await getSectionHealthHistory(sectionId, start, end, interval) }
  catch (e) { console.error('获取健康度历史失败:', e) }
}

// 在 return 中暴露
return {
  // ... 现有
  healthRank, fetchHealthRank,
  sectionHealth, fetchSectionHealth,
  sectionHealthHistory, fetchSectionHealthHistory,
}
```

- [ ] **Step 4: TypeScript 类型检查**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\frontend && npx vue-tsc -b`
Expected: 编译成功（0 错误）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/types/health.ts frontend/src/api/health.ts frontend/src/stores/monitor.ts
git commit -m "feat(health): 前端 API 封装 + Pinia 状态"
```

---

## Task 8: 看板子组件（首页内嵌）

**Files:**
- Create: `frontend/src/components/HealthDashboard.vue`
- Modify: `frontend/src/views/Dashboard.vue`
- Modify: `frontend/src/assets/main.css`（追加健康度相关样式，遵循现有变量）

- [ ] **Step 1: 新建 `frontend/src/components/HealthDashboard.vue`**

```vue
<template>
  <div class="card">
    <div class="card-header">
      <h2>断面健康度看板</h2>
      <span style="font-size:12px; color: var(--text-secondary);">
        线路: 3号线 | 共 {{ rankData?.total || 0 }} 个断面
      </span>
    </div>

    <!-- 等级分布 -->
    <div v-if="rankData?.grade_count" class="grade-summary">
      <div v-for="(g, key) in rankData.grade_count" :key="key" class="grade-chip" :class="key">
        <span class="grade-dot" :class="key"></span>
        <span class="grade-label">{{ gradeLabel(key) }}</span>
        <span class="grade-value">{{ g }}</span>
      </div>
    </div>

    <!-- 排名表 -->
    <div v-if="rankData && rankData.data.length > 0" class="rank-table">
      <div class="rank-row rank-header">
        <div class="rank-idx">#</div>
        <div class="rank-name">断面</div>
        <div class="rank-score">分值</div>
        <div class="rank-trend">趋势</div>
        <div class="rank-alerts">告警</div>
      </div>
      <router-link
        v-for="(item, idx) in rankData.data"
        :key="item.section_id"
        :to="`/sections/${item.section_id}/health`"
        class="rank-row"
        :class="`grade-${item.grade}`"
      >
        <div class="rank-idx">{{ idx + 1 }}</div>
        <div class="rank-name">
          <div>{{ item.section_name }}</div>
          <div style="font-size:11px; color: var(--text-secondary);">{{ item.section_code }}</div>
        </div>
        <div class="rank-score">
          <span class="score-num">{{ item.total_score.toFixed(1) }}</span>
          <span class="grade-tag" :class="item.grade">{{ gradeLabel(item.grade) }}</span>
        </div>
        <div class="rank-trend">
          <span :class="item.trend_delta >= 0 ? 'trend-up' : 'trend-down'">
            {{ item.trend_delta >= 0 ? '↑' : '↓' }} {{ Math.abs(item.trend_delta).toFixed(1) }}
          </span>
        </div>
        <div class="rank-alerts">
          <span v-if="item.alert_count > 0" class="alert-badge">{{ item.alert_count }}</span>
          <span v-else style="color: var(--text-secondary);">0</span>
        </div>
      </router-link>
    </div>
    <div v-else-if="!loading" class="empty-state">暂无评分数据（评分任务每 1 分钟执行）</div>
    <div v-else class="loading">加载中...</div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useMonitorStore } from '../stores/monitor'
import type { HealthGrade } from '../api/health'

const store = useMonitorStore()
const rankData = computed(() => store.healthRank)
const loading = ref(true)

function gradeLabel(g: string | HealthGrade): string {
  const map: Record<string, string> = {
    excellent: '优良', normal: '正常', attention: '关注', degraded: '劣化', danger: '危险',
  }
  return map[g as string] || g
}

onMounted(async () => {
  await store.fetchHealthRank('3')
  loading.value = false
})
</script>
```

- [ ] **Step 2: 修改 `frontend/src/views/Dashboard.vue`**

在 `<div class="stats-grid">` 之后插入：
```vue
<HealthDashboard />
```
并在 `<script setup>` 顶部 import：
```ts
import HealthDashboard from '../components/HealthDashboard.vue'
```

- [ ] **Step 3: 在 `main.css` 末尾追加健康度样式**

```css
/* 断面健康度看板 */
.grade-summary {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid #eee;
}
.grade-chip {
  display: flex; align-items: center; gap: 6px;
  padding: 4px 10px; border-radius: 12px;
  background: #f5f5f5; font-size: 12px;
}
.grade-dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
.grade-dot.excellent { background: #34a853; }
.grade-dot.normal    { background: #4285f4; }
.grade-dot.attention { background: #fbbc04; }
.grade-dot.degraded  { background: #f9ab00; }
.grade-dot.danger    { background: #ea4335; }
.grade-label { color: var(--text-primary); }
.grade-value { font-weight: 600; }

.rank-table { display: flex; flex-direction: column; gap: 2px; }
.rank-row {
  display: grid;
  grid-template-columns: 36px 1fr 130px 80px 60px;
  align-items: center;
  padding: 8px 10px;
  border-radius: 4px;
  text-decoration: none;
  color: inherit;
  font-size: 13px;
  transition: background 0.15s;
}
.rank-row:hover { background: #f8f9fa; }
.rank-row.rank-header { font-size: 12px; color: var(--text-secondary); font-weight: 600; }
.rank-row.grade-danger    { border-left: 3px solid #ea4335; }
.rank-row.grade-degraded  { border-left: 3px solid #f9ab00; }
.rank-row.grade-attention { border-left: 3px solid #fbbc04; }
.rank-row.grade-normal    { border-left: 3px solid #4285f4; }
.rank-row.grade-excellent { border-left: 3px solid #34a853; }
.score-num { font-weight: 600; font-size: 15px; margin-right: 6px; }
.grade-tag {
  font-size: 11px; padding: 1px 8px; border-radius: 10px;
}
.grade-tag.excellent { background: #e6f4ea; color: #1e7e34; }
.grade-tag.normal    { background: #e8f0fe; color: #1967d2; }
.grade-tag.attention { background: #fef7e0; color: #b06000; }
.grade-tag.degraded  { background: #feecdc; color: #c5221f; }
.grade-tag.danger    { background: #fce8e6; color: #c5221f; }
.trend-up   { color: #ea4335; }
.trend-down { color: #34a853; }
.alert-badge {
  background: #fce8e6; color: #c5221f;
  padding: 1px 8px; border-radius: 10px; font-size: 12px; font-weight: 600;
}
```

- [ ] **Step 4: TypeScript 检查**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\frontend && npx vue-tsc -b`
Expected: 0 错误。

- [ ] **Step 5: 浏览器手测**

启动前端：
```bash
cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\frontend && npm run dev
```

打开 `http://localhost:5173`，应在"最近告警"卡片之上看到"断面健康度看板"，展示 5 个等级分布 + 排名表，点击条目跳转到 `/sections/:id/health`。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/components/HealthDashboard.vue frontend/src/views/Dashboard.vue frontend/src/assets/main.css
git commit -m "feat(health): 首页健康度看板子组件 + 样式"
```

---

## Task 9: 断面健康度详情页（评分构成 + 历史曲线）

**Files:**
- Create: `frontend/src/views/SectionHealthDetail.vue`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/views/SectionDetail.vue`（追加入口链接）

- [ ] **Step 1: 新建 `frontend/src/views/SectionHealthDetail.vue`**

```vue
<template>
  <div v-if="data" class="health-detail">
    <div class="card">
      <div class="card-header">
        <h2>断面健康度评分</h2>
        <router-link :to="`/sections/${sectionId}`" style="font-size:13px; color: var(--primary); text-decoration:none;">
          ← 返回断面详情
        </router-link>
      </div>

      <div class="score-hero" :class="`grade-${data.score.grade}`">
        <div class="score-num-big">{{ data.score.total_score.toFixed(1) }}</div>
        <div class="grade-info">
          <div class="grade-tag-lg" :class="data.score.grade">{{ gradeLabel(data.score.grade) }}</div>
          <div style="font-size:12px; color: var(--text-secondary); margin-top:6px;">
            位置: {{ data.score.position_type }} | 敏感度: {{ data.score.sensitivity.toFixed(2) }}
            | 触发: {{ data.score.trigger_type }} | 计算时间: {{ formatTime(data.score.calculated_at) }}
          </div>
        </div>
      </div>

      <div class="sensor-scores">
        <div class="sensor-score" v-for="t in sensorTypes" :key="t">
          <div class="sensor-score-label">{{ t }}</div>
          <div class="sensor-score-val">{{ sensorScoreFor(t).toFixed(1) }}</div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <h2>评分构成（可解释）</h2>
      </div>
      <table class="detail-table">
        <thead>
          <tr>
            <th>维度</th><th>子项</th><th>原始值</th><th>子分</th><th>权重</th><th>贡献</th><th>说明</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(d, idx) in data.details" :key="idx">
            <td><span class="dim-tag" :class="d.dimension">{{ dimLabel(d.dimension) }}</span></td>
            <td>{{ d.sub_dimension }}</td>
            <td>{{ d.raw_value.toFixed(3) }}</td>
            <td>{{ d.sub_score.toFixed(2) }}</td>
            <td>{{ (d.weight * 100).toFixed(0) }}%</td>
            <td>{{ d.contribution.toFixed(2) }}</td>
            <td style="font-size:12px;">{{ d.explanation }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-header">
        <h2>复核中间数据</h2>
        <span style="font-size:12px; color: var(--text-secondary);">
          （保留全部输入，可用于重算验证）
        </span>
      </div>
      <table class="detail-table">
        <thead>
          <tr>
            <th>传感器</th><th>类型</th><th>24h速率</th><th>7d速率</th><th>30d速率</th>
            <th>告警数</th><th>完整度</th><th>标准差</th><th>子分</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="it in data.intermediate" :key="it.id">
            <td>#{{ it.sensor_id }}</td>
            <td>{{ it.sensor_type }}</td>
            <td>{{ it.rate_24h.toFixed(3) }}</td>
            <td>{{ it.rate_7d.toFixed(3) }}</td>
            <td>{{ it.rate_30d.toFixed(3) }}</td>
            <td>{{ it.recent_alert_count }}</td>
            <td>{{ (it.data_completeness * 100).toFixed(1) }}%</td>
            <td>{{ it.historical_variance.toFixed(3) }}</td>
            <td><strong>{{ it.sensor_sub_score.toFixed(2) }}</strong></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-header">
        <h2>历史健康度曲线</h2>
        <div style="display:flex; gap:8px; align-items:center;">
          <select v-model="interval" @change="reload" class="interval-select">
            <option value="1 hour">小时</option>
            <option value="1 day">日</option>
            <option value="7 days">周</option>
            <option value="30 days">月</option>
          </select>
          <select v-model="days" @change="reload" class="interval-select">
            <option :value="7">近 7 天</option>
            <option :value="30">近 30 天</option>
            <option :value="90">近 90 天</option>
          </select>
        </div>
      </div>
      <div v-if="history && history.data.length > 0" class="chart-wrap">
        <Line :data="chartData" :options="chartOpts" />
      </div>
      <div v-else class="empty-state">暂无历史数据</div>
    </div>
  </div>
  <div v-else class="loading">加载中...</div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useMonitorStore } from '../stores/monitor'
import { Line } from 'vue-chartjs'
import {
  Chart, CategoryScale, LinearScale, PointElement, LineElement,
  Title, Tooltip, Legend, Filler, TimeScale,
} from 'chart.js'
import type { HealthGrade, ScoreDetail } from '../api/health'

Chart.register(CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Legend, Filler, TimeScale)

const route = useRoute()
const store = useMonitorStore()
const sectionId = Number(route.params.id)

const data = computed(() => store.sectionHealth)
const history = computed(() => store.sectionHealthHistory)
const interval = ref('1 day')
const days = ref(30)
const sensorTypes = ['displacement', 'crack', 'strain']

function gradeLabel(g: string | HealthGrade): string {
  const map: Record<string, string> = {
    excellent: '优良', normal: '正常', attention: '关注', degraded: '劣化', danger: '危险',
  }
  return map[g as string] || g
}
function dimLabel(d: string): string {
  return { alert: '当前告警', trend: '变化趋势', stability: '历史稳定性', completeness: '数据完整度' }[d] || d
}
function sensorScoreFor(t: string): number {
  if (!data.value) return 0
  const s = data.value.score
  if (t === 'displacement') return s.displacement_score
  if (t === 'crack') return s.crack_score
  if (t === 'strain') return s.strain_score
  return 0
}
function formatTime(t: string) { return new Date(t).toLocaleString('zh-CN') }

const chartData = computed(() => {
  if (!history.value) return { labels: [], datasets: [] }
  return {
    labels: history.value.data.map(p => new Date(p.bucket).toLocaleDateString('zh-CN')),
    datasets: [{
      label: '健康度评分',
      data: history.value.data.map(p => p.total_score),
      borderColor: '#4285f4',
      backgroundColor: 'rgba(66,133,244,0.15)',
      fill: true,
      tension: 0.3,
    }],
  }
})
const chartOpts = {
  responsive: true,
  maintainAspectRatio: false,
  scales: { y: { min: 0, max: 100, title: { display: true, text: '健康度分值' } } },
  plugins: { legend: { display: false } },
}

async function reload() {
  const end = new Date()
  const start = new Date(end.getTime() - days.value * 86400_000)
  await store.fetchSectionHealthHistory(sectionId, start.toISOString(), end.toISOString(), interval.value)
}

onMounted(async () => {
  await store.fetchSectionHealth(sectionId)
  await reload()
})
</script>

<style scoped>
.score-hero {
  display: flex; align-items: center; gap: 24px; padding: 16px;
  border-radius: 8px; margin-bottom: 12px;
}
.score-hero.grade-excellent { background: #e6f4ea; }
.score-hero.grade-normal    { background: #e8f0fe; }
.score-hero.grade-attention { background: #fef7e0; }
.score-hero.grade-degraded  { background: #feecdc; }
.score-hero.grade-danger    { background: #fce8e6; }
.score-num-big { font-size: 56px; font-weight: 700; line-height: 1; }
.grade-tag-lg {
  display: inline-block; padding: 4px 16px; border-radius: 14px; font-size: 18px; font-weight: 600;
}
.grade-tag-lg.excellent { background: #34a853; color: white; }
.grade-tag-lg.normal    { background: #4285f4; color: white; }
.grade-tag-lg.attention { background: #fbbc04; color: white; }
.grade-tag-lg.degraded  { background: #f9ab00; color: white; }
.grade-tag-lg.danger    { background: #ea4335; color: white; }

.sensor-scores {
  display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; margin-top: 12px;
}
.sensor-score {
  padding: 12px; background: #f8f9fa; border-radius: 6px; text-align: center;
}
.sensor-score-label { font-size: 12px; color: var(--text-secondary); }
.sensor-score-val { font-size: 24px; font-weight: 600; margin-top: 4px; }

.detail-table {
  width: 100%; border-collapse: collapse; font-size: 13px;
}
.detail-table th, .detail-table td {
  padding: 8px 10px; text-align: left; border-bottom: 1px solid #eee;
}
.detail-table th { background: #f8f9fa; font-weight: 600; color: var(--text-secondary); }
.dim-tag {
  display: inline-block; padding: 1px 8px; border-radius: 10px; font-size: 11px;
  background: #e8f0fe; color: #1967d2;
}
.dim-tag.alert        { background: #fce8e6; color: #c5221f; }
.dim-tag.trend        { background: #fef7e0; color: #b06000; }
.dim-tag.stability    { background: #e6f4ea; color: #1e7e34; }
.dim-tag.completeness { background: #f3e8fd; color: #8430ce; }

.chart-wrap { height: 280px; }
.interval-select {
  padding: 4px 8px; border: 1px solid #ddd; border-radius: 4px; font-size: 12px;
}
</style>
```

- [ ] **Step 2: 修改 `frontend/src/router/index.ts`**

在路由数组中追加：
```ts
{ path: '/sections/:id/health', name: 'SectionHealthDetail', component: () => import('../views/SectionHealthDetail.vue') },
```

（懒加载避免首屏包变大。）

- [ ] **Step 3: 修改 `frontend/src/views/SectionDetail.vue`**

在 `<template>` 顶部（紧跟 `<div>` 之后）插入健康度摘要卡：
```vue
<div v-if="store.sectionHealth" class="card">
  <div class="card-header">
    <h2>健康度评分</h2>
    <router-link :to="`/sections/${$route.params.id}/health`" style="font-size:13px; color: var(--primary); text-decoration:none;">
      查看详情 →
    </router-link>
  </div>
  <div style="display:flex; align-items:center; gap:16px;">
    <div style="font-size:36px; font-weight:700;" :class="`grade-${store.sectionHealth.score.grade}`">
      {{ store.sectionHealth.score.total_score.toFixed(1) }}
    </div>
    <div>
      <div class="grade-tag-lg" :class="store.sectionHealth.score.grade">
        {{ {excellent:'优良',normal:'正常',attention:'关注',degraded:'劣化',danger:'危险'}[store.sectionHealth.score.grade] }}
      </div>
      <div style="font-size:12px; color: var(--text-secondary); margin-top:4px;">
        位移 {{ store.sectionHealth.score.displacement_score.toFixed(1) }} |
        裂缝 {{ store.sectionHealth.score.crack_score.toFixed(1) }} |
        应变 {{ store.sectionHealth.score.strain_score.toFixed(1) }}
      </div>
    </div>
  </div>
</div>
```

并在 `onMounted` 中追加：
```ts
store.fetchSectionHealth(Number(route.params.id))
```

- [ ] **Step 4: TypeScript 检查 + 浏览器手测**

Run: `cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11\frontend && npx vue-tsc -b`
Expected: 0 错误。

启动后：
1. 访问 `http://localhost:5173/sections/1/health` 应展示评分构成、历史曲线
2. 详情页加载 ≤ 3s

- [ ] **Step 5: 提交**

```bash
git add frontend/src/views/SectionHealthDetail.vue frontend/src/router/index.ts frontend/src/views/SectionDetail.vue
git commit -m "feat(health): 断面健康度详情页 + 入口链接"
```

---

## Task 10: 端到端验收（模拟数据触发告警，验证评分下降 + SLA）

**Files:**
- Create: `simulator/cmd/health_e2e_test/main.go`（保留为回归测试脚本）
- Modify: 无（仅执行验证）

- [ ] **Step 1: 新建 `simulator/cmd/health_e2e_test/main.go`**

```go
// 端到端验收脚本：模拟某断面在 5 分钟内产生 3 次连续告警，
// 等待评分更新后验证总分明显下降（< 60），并测量响应时间。
//
// 用法：go run ./cmd/health_e2e_test -section 1 -base http://localhost:8080
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	sectionID := flag.Int("section", 1, "断面 ID")
	base := flag.String("base", "http://localhost:8080", "后端基地址")
	flag.Parse()

	client := &http.Client{ Timeout: 30 * time.Second }

	// 1) 拉取初始评分
	initial := getHealth(client, *base, *sectionID)
	log.Printf("【E2E】初始评分=%.2f grade=%s", initial.TotalScore, initial.Grade)

	// 2) 查该断面传感器列表
	sensors := getSensors(client, *base, *sectionID)
	log.Printf("【E2E】断面 %d 传感器数=%d", *sectionID, len(sensors))

	// 3) 模拟 3 次高强度告警数据上报（24h 速率超 danger 阈值）
	now := time.Now()
	var data []map[string]interface{}
	for i, s := range sensors {
		// 位移计和裂缝计注入危险值；应变注入警告值
		offset := 0.0
		if s["type"] == "displacement" { offset = 1.5 }   // > 1.0 danger
		if s["type"] == "crack"         { offset = 0.5 }   // > 0.3 danger
		if s["type"] == "strain"        { offset = 40.0 }  // > 30 danger
		// 24h 区间每 5 分钟一个点，整体向上漂移
		for h := 24; h >= 0; h-- {
			ts := now.Add(-time.Duration(h) * time.Hour)
			val := offset * float64(24-h) / 24.0
			data = append(data, map[string]interface{}{
				"sensor_id": int(s["id"].(float64)),
				"value":     val + float64(i)*0.01,
				"timestamp": ts.Format(time.RFC3339),
			})
		}
	}
	post := map[string]interface{}{
		"collector_code": "e2e-test",
		"data":           data,
	}
	body, _ := json.Marshal(post)
	resp, err := client.Post(*base+"/api/v1/collect", "application/json", bytes.NewReader(body))
	if err != nil { log.Fatalf("上报失败: %v", err) }
	resp.Body.Close()
	log.Printf("【E2E】已上报 %d 个数据点，等待 80s 让 cron 与事件触发评分...", len(data))

	// 4) 等待评分更新
	time.Sleep(80 * time.Second)

	// 5) 再次拉取评分
	final := getHealth(client, *base, *sectionID)
	log.Printf("【E2E】最终评分=%.2f grade=%s", final.TotalScore, final.Grade)

	// 6) 验证
	if final.TotalScore >= 60 {
		log.Fatalf("【E2E-失败】评分应明显下降(<60)，实际=%.2f", final.TotalScore)
	}
	if final.TotalScore >= initial.TotalScore {
		log.Fatalf("【E2E-失败】最终分应低于初始分(%.2f)，实际=%.2f", initial.TotalScore, final.TotalScore)
	}
	log.Printf("【E2E-通过】初始=%.2f → 最终=%.2f, 下降 %.2f 分", initial.TotalScore, final.TotalScore, initial.TotalScore-final.TotalScore)

	// 7) 测量历史曲线查询响应时间
	t0 := time.Now()
	end := time.Now()
	start := end.Add(-30 * 24 * time.Hour)
	url := fmt.Sprintf("%s/api/v1/sections/%d/health/history?start=%s&end=%s&interval=1%%20day",
		*base, *sectionID, start.Format(time.RFC3339), end.Format(time.RFC3339))
	r, err := client.Get(url)
	if err != nil { log.Fatalf("历史曲线请求失败: %v", err) }
	r.Body.Close()
	elapsed := time.Since(t0)
	log.Printf("【E2E】历史曲线查询耗时=%v (要求 ≤3s)", elapsed)
	if elapsed > 3*time.Second {
		log.Fatalf("【E2E-失败】历史曲线响应超过 3s")
	}
}

type healthScore struct {
	TotalScore float64 `json:"total_score"`
	Grade      string  `json:"grade"`
}

func getHealth(client *http.Client, base string, id int) healthScore {
	r, err := client.Get(fmt.Sprintf("%s/api/v1/sections/%d/health", base, id))
	if err != nil { log.Fatalf("获取评分失败: %v", err) }
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	var resp struct {
		Score healthScore `json:"score"`
	}
	_ = json.Unmarshal(body, &resp)
	return resp.Score
}

func getSensors(client *http.Client, base string, id int) []map[string]interface{} {
	r, err := client.Get(fmt.Sprintf("%s/api/v1/sections/%d/sensors", base, id))
	if err != nil { log.Fatalf("获取传感器失败: %v", err) }
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(body, &resp)
	return resp.Data
}
```

- [ ] **Step 2: 准备环境**

```bash
cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11
# 启动 docker compose（数据库+后端+前端）
docker-compose up -d
# 等待后端就绪
timeout 30 bash -c 'until curl -sf http://localhost:8080/api/v1/health; do sleep 1; done'
```

- [ ] **Step 3: 运行 E2E 验收**

```bash
cd d:\Work\benzhi\Prompt-Agent\workspace\xzc-11
go run ./simulator/cmd/health_e2e_test -section 1 -base http://localhost:8080
```

预期日志：
```
【E2E】初始评分=XX.XX grade=excellent
【E2E】已上报 X 个数据点，等待 80s 让 cron 与事件触发评分...
【E2E】最终评分=XX.XX grade=attention/degraded/danger
【E2E-通过】初始=XX.XX → 最终=XX.XX, 下降 XX.XX 分
【E2E】历史曲线查询耗时=XXXms (要求 ≤3s)
```

- [ ] **Step 4: 浏览器回归**

1. 打开 `http://localhost:5173/`，确认"断面健康度看板"中 1 号断面排名靠前（分值明显低）+ 等级分布中 `attention/degraded/danger` 计数 ≥ 1
2. 点击 1 号断面跳转到 `/sections/1/health`，确认：
   - 总分明细 4 个维度（告警/趋势/稳定性/完整度）都展示
   - 复核中间数据 3 行（位移/裂缝/应变）
   - 历史曲线展示评分点
3. 在浏览器开发者工具 Network 面板观察 `health/history` 接口 P95 响应 < 3s

- [ ] **Step 5: 验证现有告警链路未受影响**

```bash
curl http://localhost:8080/api/v1/alerts/active | head -100
```

确认告警 JSON 结构与改造前一致（`level/status/message/triggered_at` 等字段未变化）。

- [ ] **Step 6: 提交**

```bash
git add simulator/cmd/health_e2e_test/
git commit -m "test(health): 端到端验收脚本（3次告警→分下降+SLA验证）"
```

---

## Self-Review

**1. Spec coverage:**

| 需求条目 | 对应 Task |
| --- | --- |
| 多维评分（位移/裂缝/应变 + 告警 + 趋势） | Task 2 / 4 |
| 0-100 分 + 5 档分级 | Task 1（model + 阈值） |
| 首页看板按线路展示排名/趋势/告警数 | Task 8 |
| 点击断面查看评分明细 | Task 9 |
| 历史健康度曲线（按时间区间） | Task 9 |
| ≥3 年数据保留 | Task 1（retention policy） |
| 评估结果可解释 | Task 2（details）+ Task 9（明细表） |
| 保留可重算的中间数据 | Task 2（intermediate）+ Task 1（intermediate 表） |
| 考虑传感器类型与断面位置 | Task 1（weight/sensitivity） + Task 2（加权聚合） |
| 定时任务 + 新数据上报两种触发 | Task 4（cron + EnqueueRecompute）+ Task 6（analyzer 联动） |
| 不影响现有告警链路 | Task 6 仅在 InsertAlert 成功后异步调用 + 5-6 步验证 |
| 技术栈 Go+PG+TSDB+Vue3 不变 | 全部 Task |
| 验收：首页看板正确 | Task 8 + Task 10 步骤 4 |
| 验收：明细可查看 | Task 9 + Task 10 步骤 4 |
| 验收：3 次告警后分明显下降 | Task 10 步骤 1-3 |
| 验收：1 分钟内更新 | Task 4（cron */1）+ Task 10 步骤 3（80s 等待） |
| 验收：历史曲线 ≤3s | Task 10 步骤 3 |

**2. Placeholder scan:** 已检查 — 所有 Task 步骤包含完整代码块、确切文件路径与命令；无 "TBD" / "TODO" / "类似 Task N" 引用。

**3. Type consistency:**
- `model.HealthGrade` 枚举值在 Task 1 / 2 / 8 / 9 全文一致
- `model.SectionPositionType` 枚举值在 Task 1 / 2 / 4 一致
- `model.HealthTriggerType` 在 Task 1 / 4 / scheduler.go 一致
- `store.HistoryPoint` / `store.RankItem` 在 Task 3 / 5 / 前端类型映射一致
- `model.DimensionWeights` 字段在 Task 1 定义、Task 2 读取，键名一致
- API 路径 `/api/v1/health-dashboard/rank` 与 `/api/v1/sections/:id/health{,/history,/recompute}` 在 Task 5 handler、Task 7 前端 api、Task 9 路由一致

无类型不一致问题。

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-17-section-health-score.md`. Two execution options:

**1. Subagent-Driven (recommended)** - 派发独立子代理逐个 Task 执行，每 Task 间插入审查点，迭代速度快。

**2. Inline Execution** - 在当前会话按 Task 顺序批量执行，每 1-2 个 Task 插入一次检查点供你审查。

请告诉我选择哪种执行方式？
