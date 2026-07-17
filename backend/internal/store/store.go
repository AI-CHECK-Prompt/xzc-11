package store

import (
	"context"
	"fmt"
	"math"
	"time"
	"tunnel-shm/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 速率分析关键参数（包级常量，便于测试和调整）
const (
	// SlidingWindow 滑动窗口时长：用于捕捉窗口内的瞬时剧烈波动
	// 1 小时是隧道监测的常用瞬时分析窗口，可识别阶跃跳变
	SlidingWindow = 1 * time.Hour
	// MinStepInterval 相邻点阶跃检测的最小时间间隔，避免单点噪声被误判
	MinStepInterval = 1 * time.Minute
)

// Store 数据库操作层
type Store struct {
	pool *pgxpool.Pool
}

// New 创建Store实例
func New(ctx context.Context, connStr string) (*Store, error) {
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("解析连接字符串失败: %w", err)
	}
	config.MaxConns = 50
	config.MinConns = 5

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Close 关闭连接池
func (s *Store) Close() {
	s.pool.Close()
}

// InitSchema 初始化数据库表结构
//
// 同步执行建表与列迁移：
//   - 新部署：完整执行 schema 中的 CREATE TABLE
//   - 已有库：检查 alerts.type 列是否存在，缺失则 ALTER TABLE 补齐
//     （用于向后兼容存量部署，避免新字段上线时报"column does not exist"）
func (s *Store) InitSchema(ctx context.Context) error {
	schema := `
	-- 创建TimescaleDB扩展
	CREATE EXTENSION IF NOT EXISTS timescaledb;

	-- 监测断面表
	CREATE TABLE IF NOT EXISTS sections (
		id SERIAL PRIMARY KEY,
		code VARCHAR(50) UNIQUE NOT NULL,
		name VARCHAR(200) NOT NULL,
		line_code VARCHAR(20) NOT NULL DEFAULT '3',
		station_km INTEGER NOT NULL,
		description TEXT DEFAULT '',
		location_lat DOUBLE PRECISION DEFAULT 0,
		location_lng DOUBLE PRECISION DEFAULT 0
	);

	-- 传感器表
	CREATE TABLE IF NOT EXISTS sensors (
		id SERIAL PRIMARY KEY,
		section_id INTEGER NOT NULL REFERENCES sections(id),
		code VARCHAR(50) UNIQUE NOT NULL,
		type VARCHAR(20) NOT NULL,
		position VARCHAR(50) NOT NULL DEFAULT '',
		calibration DOUBLE PRECISION NOT NULL DEFAULT 1.0
	);

	-- 传感器时序数据表（TimescaleDB hypertable）
	CREATE TABLE IF NOT EXISTS sensor_data (
		id SERIAL,
		sensor_id INTEGER NOT NULL,
		value DOUBLE PRECISION NOT NULL,
		timestamp TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (id, timestamp)
	);

	-- 转换为hypertable，按7天分区
	SELECT create_hypertable('sensor_data', 'timestamp', if_not_exists => TRUE,
		chunk_time_interval => INTERVAL '7 days');

	-- 传感器数据索引
	CREATE INDEX IF NOT EXISTS idx_sensor_data_sensor_id_time
		ON sensor_data (sensor_id, timestamp DESC);

	-- 告警表
	CREATE TABLE IF NOT EXISTS alerts (
		id SERIAL PRIMARY KEY,
		section_id INTEGER NOT NULL,
		sensor_id INTEGER NOT NULL,
		level VARCHAR(20) NOT NULL,
		type VARCHAR(20) NOT NULL DEFAULT 'rate',
		message TEXT NOT NULL,
		deformation_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
		threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
		status VARCHAR(20) NOT NULL DEFAULT 'active',
		triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		resolved_at TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts (status);
	CREATE INDEX IF NOT EXISTS idx_alerts_section ON alerts (section_id, triggered_at DESC);
	-- 告警类型索引：用于存活感知 cron 判重（offline 类型 30 分钟内不重复）
	CREATE INDEX IF NOT EXISTS idx_alerts_sensor_type_time ON alerts (sensor_id, type, triggered_at DESC);

	-- 数据保留策略：自动删除3年前的数据
	SELECT add_retention_policy('sensor_data', INTERVAL '3 years', if_not_exists => TRUE);

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
	_, err := s.pool.Exec(ctx, schema)
	if err != nil {
		return err
	}

	// 兼容历史库：alerts 表缺 type 列时补齐（存活感知告警需要该字段）
	// 采用 IF NOT EXISTS 语义：PostgreSQL 不支持 ADD COLUMN IF NOT EXISTS 旧版本，
	// 这里通过查询 information_schema 自行判断后再 ALTER，避免在已升级库上抛错。
	_, err = s.pool.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'alerts' AND column_name = 'type'
			) THEN
				ALTER TABLE alerts ADD COLUMN type VARCHAR(20) NOT NULL DEFAULT 'rate';
			END IF;
		END $$;
		CREATE INDEX IF NOT EXISTS idx_alerts_sensor_type_time
			ON alerts (sensor_id, type, triggered_at DESC);
	`)
	return err
}

// InsertSensorDataBatch 批量插入传感器数据
func (s *Store) InsertSensorDataBatch(ctx context.Context, data []model.SensorData) error {
	if len(data) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, d := range data {
		batch.Queue(
			`INSERT INTO sensor_data (sensor_id, value, timestamp)
			 VALUES ($1, $2, $3)`,
			d.SensorID, d.Value, d.Timestamp,
		)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for i := 0; i < len(data); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("批量插入第%d条失败: %w", i+1, err)
		}
	}
	return nil
}

// GetLatestSensorData 获取传感器最新数据
func (s *Store) GetLatestSensorData(ctx context.Context, sensorID int) (*model.SensorData, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, sensor_id, value, timestamp
		 FROM sensor_data
		 WHERE sensor_id = $1
		 ORDER BY timestamp DESC
		 LIMIT 1`, sensorID)

	var d model.SensorData
	err := row.Scan(&d.ID, &d.SensorID, &d.Value, &d.Timestamp)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetLatestSectionData 获取断面所有传感器最新数据
func (s *Store) GetLatestSectionData(ctx context.Context, sectionID int) ([]model.SensorData, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT ON (sd.sensor_id) sd.id, sd.sensor_id, sd.value, sd.timestamp
		 FROM sensor_data sd
		 JOIN sensors se ON sd.sensor_id = se.id
		 WHERE se.section_id = $1
		 ORDER BY sd.sensor_id, sd.timestamp DESC`, sectionID)
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

// GetHistoricalData 获取传感器历史数据
func (s *Store) GetHistoricalData(ctx context.Context, sensorID int, start, end time.Time, limit int) ([]model.SensorData, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, sensor_id, value, timestamp
		 FROM sensor_data
		 WHERE sensor_id = $1 AND timestamp >= $2 AND timestamp <= $3
		 ORDER BY timestamp ASC
		 LIMIT $4`, sensorID, start, end, limit)
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

// GetHistoricalDataAggregated 获取聚合后的历史数据（用于趋势图）
//
// 修复"阶梯状跳变"bug：原实现使用 time_bucket(interval, timestamp) 3 参数版本，
// 对齐到 1970-01-01 00:00:00 UTC + N*interval（UTC 整点）。当查询区间超过 24h
// 且使用按小时聚合时，bucket 边界（UTC 整点）会与 sensor_data 实际数据点
// （simulator 启动时刻 + N*60s，与 UTC 整点不对齐）产生 23m45s 量级的偏差，
// 导致：
//   1) 第一个 bucket 实际只覆盖 23m45s ~ 59m59s（样本数 37），样本不足；
//   2) 最后一个 bucket 同样只覆盖 00m00s ~ 23m45s（样本数 24），样本不足；
//   3) 中间每个整点 bucket 边界与数据点不对齐，使得前端曲线呈现
//      "每隔一个点突然跳到几小时前的值再缓慢爬升"的阶梯状错位感；
//   4) 当前端 labels 用 toLocaleString('zh-CN') 格式化 bucket 起点后，
//      错位的 bucket 时间戳会让用户误读趋势。
//
// 修复方案：使用 time_bucket 4 参数版本，把查询区间的 start 作为 origin。
// 这样 bucket 起点 = start + N*interval，与 sensor_data 实际数据点严格对齐，
// 每个完整 bucket 都恰好覆盖 60 条 60s 周期的连续样本，AVG 曲线恢复平滑，
// 与 sensor_data 真实漂移趋势一致。
//
// 注意：origin 取自 $2（即 WHERE 条件中的 start），保证 bucket 边界与
// 查询窗口严格对齐，不会因 UTC 整点偏移产生累积偏差。
func (s *Store) GetHistoricalDataAggregated(ctx context.Context, sensorID int, start, end time.Time, interval string) ([]model.SensorData, error) {
	query := fmt.Sprintf(
		`SELECT
			MIN(id) as id,
			sensor_id,
			AVG(value) as value,
			time_bucket('%s'::interval, timestamp, $2) as bucket
		 FROM sensor_data
		 WHERE sensor_id = $1 AND timestamp >= $2 AND timestamp <= $3
		 GROUP BY bucket, sensor_id
		 ORDER BY bucket ASC`, interval)

	rows, err := s.pool.Query(ctx, query, sensorID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data []model.SensorData
	for rows.Next() {
		var d model.SensorData
		var bucket time.Time
		if err := rows.Scan(&d.ID, &d.SensorID, &d.Value, &bucket); err != nil {
			return nil, err
		}
		d.Timestamp = bucket
		data = append(data, d)
	}
	return data, rows.Err()
}

// CalculateDeformationRate 计算 24 小时窗口内的变形速率（mm/天）
//
// 修正历史缺陷：原实现仅取窗口首末两点做差，未考虑中间过程的阶跃跳变
// 与反复波动，无法识别"先抬升后回落"导致净变化小但瞬时风险极高的情况
// （如位移在 1h 内从 12.3mm 抬升至 14.8mm，瞬时速率达 6mm/天，远超阈值）。
//
// 新实现采用多窗口分析，取三类速率中的最严值（绝对值最大）作为告警判定：
//   1) EndpointRate  端点速率：(末值-首值)/实际时长*24h（兼容历史逻辑）
//   2) MaxSlidingRate 1h 滑动窗口内的最大瞬时变化率（捕捉阶跃/波动）
//   3) MaxStepRate   相邻数据点间的最大阶跃速率（捕捉单点突变）
//
// 实际计算逻辑下沉到 AnalyzeRateFromData 纯函数，便于单元测试。
func (s *Store) CalculateDeformationRate(ctx context.Context, sensorID int) (*model.DeformationRate, error) {
	now := time.Now()
	windowStart := now.Add(-24 * time.Hour)

	rows, err := s.pool.Query(ctx,
		`SELECT id, sensor_id, value, timestamp
		 FROM sensor_data
		 WHERE sensor_id = $1 AND timestamp >= $2 AND timestamp <= $3
		 ORDER BY timestamp ASC`, sensorID, windowStart, now)
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

	return AnalyzeRateFromData(sensorID, data)
}

// AnalyzeRateFromData 纯函数：对一组有序传感器数据计算多窗口速率
// data 必须按时间升序、至少 2 个点；返回的 DeformationRate 中
//   - Rate: 三类速率绝对值最大者（mm/天，已归一化）
//   - RateSource: 该最严速率的来源（endpoint / sliding / step）
//
// 抽取该函数的目的：与数据库 I/O 解耦，便于单元测试覆盖典型场景
// （阶跃跳变、缓慢漂移、噪声、单点突变、首末相消等）。
func AnalyzeRateFromData(sensorID int, data []model.SensorData) (*model.DeformationRate, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("数据点不足")
	}

	first := data[0]
	last := data[len(data)-1]

	// (1) 端点速率（mm/天）：兼容历史告警判定逻辑
	var endpointRate float64
	if hours := last.Timestamp.Sub(first.Timestamp).Hours(); hours > 0 {
		endpointRate = (last.Value - first.Value) / hours * 24.0
	}

	// (2) 滑动窗口最大瞬时速率：对每个数据点向后续查找首个时间差
	//     >= SlidingWindow 的点，计算两点间的归一化变化率；保留绝对值最大者。
	//     该策略能精确捕捉 1h 内的阶跃抬升/回落场景。
	var (
		maxSlidingRate   float64
		slidingStartVal  float64
		slidingEndVal    float64
		slidingStartTime time.Time
		slidingEndTime   time.Time
	)
	for i := 0; i < len(data); i++ {
		for j := i + 1; j < len(data); j++ {
			span := data[j].Timestamp.Sub(data[i].Timestamp)
			if span < SlidingWindow {
				continue
			}
			hours := span.Hours()
			if hours <= 0 {
				break
			}
			rate := (data[j].Value - data[i].Value) / hours * 24.0
			if math.Abs(rate) > math.Abs(maxSlidingRate) {
				maxSlidingRate = rate
				slidingStartTime = data[i].Timestamp
				slidingEndTime = data[j].Timestamp
				slidingStartVal = data[i].Value
				slidingEndVal = data[j].Value
			}
			break // 已找到 i 之后首个达到窗口的点
		}
	}

	// (3) 相邻点阶跃最大速率：捕捉单点突变，限定最小间隔避免噪声放大
	var (
		maxStepRate float64
		stepFromVal float64
		stepToVal   float64
		stepFrom    time.Time
		stepTo      time.Time
	)
	for i := 1; i < len(data); i++ {
		span := data[i].Timestamp.Sub(data[i-1].Timestamp)
		if span < MinStepInterval {
			continue
		}
		hours := span.Hours()
		if hours <= 0 {
			continue
		}
		rate := (data[i].Value - data[i-1].Value) / hours * 24.0
		if math.Abs(rate) > math.Abs(maxStepRate) {
			maxStepRate = rate
			stepFrom = data[i-1].Timestamp
			stepTo = data[i].Timestamp
			stepFromVal = data[i-1].Value
			stepToVal = data[i].Value
		}
	}

	// (4) 取最严速率作为告警判定依据，并记录其来源
	rate := endpointRate
	source := model.RateSourceEndpoint
	if math.Abs(maxSlidingRate) > math.Abs(rate) {
		rate = maxSlidingRate
		source = model.RateSourceSlidingWin
	}
	if math.Abs(maxStepRate) > math.Abs(rate) {
		rate = maxStepRate
		source = model.RateSourceStep
	}

	// (5) 窗口内极值统计（用于消息展示瞬时跨度）
	minVal, maxVal := data[0].Value, data[0].Value
	for _, d := range data[1:] {
		if d.Value < minVal {
			minVal = d.Value
		}
		if d.Value > maxVal {
			maxVal = d.Value
		}
	}

	result := &model.DeformationRate{
		SensorID:   sensorID,
		Rate:       rate,
		RateSource: source,

		StartTime:  first.Timestamp,
		EndTime:    last.Timestamp,
		DataPoints: len(data),
		LastValue:  last.Value,
		FirstValue: first.Value,

		EndpointRate: endpointRate,

		MaxSlidingRate:   maxSlidingRate,
		SlidingWindow:    SlidingWindow.String(),
		SlidingStartTime: slidingStartTime,
		SlidingEndTime:   slidingEndTime,
		SlidingStartVal:  slidingStartVal,
		SlidingEndVal:    slidingEndVal,

		MaxStepRate:  maxStepRate,
		StepFromTime: stepFrom,
		StepToTime:   stepTo,
		StepFromVal:  stepFromVal,
		StepToVal:    stepToVal,

		MinValue: minVal,
		MaxValue: maxVal,
	}

	return result, nil
}

// InsertAlert 插入告警
//
// type 字段用于区分告警触发原因（rate=速率超阈值/offline=设备离线），
// 缺省 'rate'，保持与历史告警数据兼容。
func (s *Store) InsertAlert(ctx context.Context, alert *model.Alert) error {
	alertType := alert.Type
	if alertType == "" {
		alertType = model.AlertTypeRate
	}
	return s.pool.QueryRow(ctx,
		`INSERT INTO alerts (section_id, sensor_id, level, type, message, deformation_rate, threshold, status, triggered_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id`,
		alert.SectionID, alert.SensorID, alert.Level, alertType, alert.Message,
		alert.DeformationRate, alert.Threshold, alert.Status, alert.TriggeredAt,
	).Scan(&alert.ID)
}

// GetActiveAlerts 获取活跃告警
func (s *Store) GetActiveAlerts(ctx context.Context) ([]model.Alert, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, section_id, sensor_id, level, type, message, deformation_rate, threshold, status, triggered_at, resolved_at
		 FROM alerts
		 WHERE status = 'active'
		 ORDER BY triggered_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.SectionID, &a.SensorID, &a.Level, &a.Type, &a.Message,
			&a.DeformationRate, &a.Threshold, &a.Status, &a.TriggeredAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// GetSectionAlerts 获取断面告警
// status 传空字符串表示不过滤状态（兼容历史调用方）；
// 传入 "active" / "resolved" 时按状态精确过滤，避免实时面板混入已解决告警。
func (s *Store) GetSectionAlerts(ctx context.Context, sectionID int, limit int, status string) ([]model.Alert, error) {
	query := `SELECT id, section_id, sensor_id, level, type, message, deformation_rate, threshold, status, triggered_at, resolved_at
		 FROM alerts
		 WHERE section_id = $1`
	args := []interface{}{sectionID}
	if status != "" {
		query += ` AND status = $2`
		args = append(args, status)
		query += ` ORDER BY triggered_at DESC LIMIT $3`
		args = append(args, limit)
	} else {
		query += ` ORDER BY triggered_at DESC LIMIT $2`
		args = append(args, limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.SectionID, &a.SensorID, &a.Level, &a.Type, &a.Message,
			&a.DeformationRate, &a.Threshold, &a.Status, &a.TriggeredAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// ResolveAlert 解决告警
func (s *Store) ResolveAlert(ctx context.Context, alertID int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE alerts SET status = 'resolved', resolved_at = NOW()
		 WHERE id = $1`, alertID)
	return err
}

// CountActiveAlertsBySection 统计每个断面的当前活跃告警数（status='active'）
//
// 用途：首页"监测断面概览"卡片需要展示"当前告警数"小红标。
// 必须与详情页 GetSectionAlerts(id, ..., 'active') 的口径保持一致——
// 仅统计 status='active' 的告警，已自动恢复 / 人工解决的告警不计。
//
// 返回值：sectionID -> count。无活跃告警的断面不会出现在 map 中。
// 性能：单条 GROUP BY 聚合，命中 idx_alerts_section / idx_alerts_status。
func (s *Store) CountActiveAlertsBySection(ctx context.Context) (map[int]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT section_id, COUNT(*)::int
		 FROM alerts
		 WHERE status = 'active'
		 GROUP BY section_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]int)
	for rows.Next() {
		var sectionID, count int
		if err := rows.Scan(&sectionID, &count); err != nil {
			return nil, err
		}
		result[sectionID] = count
	}
	return result, rows.Err()
}

// AutoResolveAlerts 批量自动关闭告警（按 ID 列表）
//
// 用于自动恢复流程：定时任务判定"数据已恢复"后，调用本方法将
// 一批 active 告警一次性更新为 resolved。更新条件带 status='active' 兜底，
// 避免重复关闭同一告警（若两次定时任务并发执行，后执行的将是 0 行更新）。
//
// 返回值：成功关闭的告警条数（用于上层统计）
func (s *Store) AutoResolveAlerts(ctx context.Context, alertIDs []int) (int, error) {
	if len(alertIDs) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE alerts
		 SET status = 'resolved', resolved_at = NOW()
		 WHERE status = 'active' AND id = ANY($1)`,
		alertIDs)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// GetSections 获取所有断面
func (s *Store) GetSections(ctx context.Context) ([]model.Section, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, code, name, line_code, station_km, description, location_lat, location_lng, position_type
		 FROM sections ORDER BY station_km ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sections []model.Section
	for rows.Next() {
		var sec model.Section
		if err := rows.Scan(&sec.ID, &sec.Code, &sec.Name, &sec.LineCode,
			&sec.StationKm, &sec.Description, &sec.LocationLat, &sec.LocationLng, &sec.PositionType); err != nil {
			return nil, err
		}
		sections = append(sections, sec)
	}
	return sections, rows.Err()
}

// GetSection 获取单个断面
func (s *Store) GetSection(ctx context.Context, id int) (*model.Section, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, code, name, line_code, station_km, description, location_lat, location_lng, position_type
		 FROM sections WHERE id = $1`, id)

	var sec model.Section
	err := row.Scan(&sec.ID, &sec.Code, &sec.Name, &sec.LineCode,
		&sec.StationKm, &sec.Description, &sec.LocationLat, &sec.LocationLng, &sec.PositionType)
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

// GetSensorsBySection 获取断面下所有传感器
func (s *Store) GetSensorsBySection(ctx context.Context, sectionID int) ([]model.Sensor, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, section_id, code, type, position, calibration
		 FROM sensors WHERE section_id = $1`, sectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sensors []model.Sensor
	for rows.Next() {
		var se model.Sensor
		if err := rows.Scan(&se.ID, &se.SectionID, &se.Code, &se.Type, &se.Position, &se.Calibration); err != nil {
			return nil, err
		}
		sensors = append(sensors, se)
	}
	return sensors, rows.Err()
}

// GetSensor 获取传感器信息
func (s *Store) GetSensor(ctx context.Context, sensorID int) (*model.Sensor, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, section_id, code, type, position, calibration
		 FROM sensors WHERE id = $1`, sensorID)

	var se model.Sensor
	err := row.Scan(&se.ID, &se.SectionID, &se.Code, &se.Type, &se.Position, &se.Calibration)
	if err != nil {
		return nil, err
	}
	return &se, nil
}

// GetSensorByCode 根据编码获取传感器
func (s *Store) GetSensorByCode(ctx context.Context, code string) (*model.Sensor, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, section_id, code, type, position, calibration
		 FROM sensors WHERE code = $1`, code)

	var se model.Sensor
	err := row.Scan(&se.ID, &se.SectionID, &se.Code, &se.Type, &se.Position, &se.Calibration)
	if err != nil {
		return nil, err
	}
	return &se, nil
}

// GetSensorsCalibrationByIDs 批量获取多个传感器的校准系数
// 用于采集接口：RS485 采集器上报数据后，必须按每台传感器的
// calibration 系数做修正后再入库，避免出现"出厂系数 1.05 入库 1.0"
// 这类系统性偏差。返回值为 sensorID -> calibration 映射；
// 找不到的 sensorID 不会出现在结果中，调用方应按 1.0 默认处理。
func (s *Store) GetSensorsCalibrationByIDs(ctx context.Context, sensorIDs []int) (map[int]float64, error) {
	if len(sensorIDs) == 0 {
		return map[int]float64{}, nil
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, calibration FROM sensors WHERE id = ANY($1)`, sensorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int]float64, len(sensorIDs))
	for rows.Next() {
		var id int
		var cal float64
		if err := rows.Scan(&id, &cal); err != nil {
			return nil, err
		}
		result[id] = cal
	}
	return result, rows.Err()
}

// GetSensorWithSection 获取传感器及其所属断面信息
func (s *Store) GetSensorWithSection(ctx context.Context, sensorID int) (*model.Sensor, *model.Section, error) {
	sensor, err := s.GetSensor(ctx, sensorID)
	if err != nil {
		return nil, nil, err
	}
	section, err := s.GetSection(ctx, sensor.SectionID)
	if err != nil {
		return nil, nil, err
	}
	return sensor, section, nil
}

// CheckRecentAlert 检查最近是否有相同告警（防止重复告警）
//
// type 传空字符串时不过滤类型（兼容历史调用方），用于"速率超阈值"告警；
// 传入 AlertTypeOffline 后只查同类型告警，避免与"设备离线"告警互相抑制。
func (s *Store) CheckRecentAlert(ctx context.Context, sensorID int, level model.AlertLevel, withinMinutes int, alertType model.AlertType) (bool, error) {
	query := `SELECT COUNT(*) FROM alerts
		 WHERE sensor_id = $1 AND level = $2 AND status = 'active'
		 AND triggered_at > NOW() - INTERVAL '1 minute' * $3`
	args := []interface{}{sensorID, level, withinMinutes}
	if alertType != "" {
		query += ` AND type = $4`
		args = append(args, alertType)
	}
	var count int
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// ===========================================
// 传感器存活感知（liveness / online status）
// ===========================================

// 存活感知关键阈值（包级常量，便于测试和调整）
//
// 设计依据：
//   - 业务约定采样周期为 5 分钟（见 healthscore.scheduler 中 expected := 7*24*12）
//   - 10 分钟无数据：可能是网络瞬抖，不告警但前端标"亚健康"提示运维关注
//   - 30 分钟无数据：连续 6 个周期缺失，触发"数据缺失"告警（warning）
//   - 120 分钟无数据：连续 24 个周期缺失，已远超日常维护周期，升级为 danger
const (
	// SensorOnlineThreshold  在线判定上限：最近一次数据距今不超过该值
	SensorOnlineThreshold = 10 * time.Minute
	// SensorStaleThreshold   亚健康判定上限：[online, stale) 为亚健康
	SensorStaleThreshold = 30 * time.Minute
	// SensorOfflineWarningThreshold 离线告警触发阈值（warning）
	SensorOfflineWarningThreshold = 30 * time.Minute
	// SensorOfflineDangerThreshold  离线告警升级阈值（danger）
	SensorOfflineDangerThreshold = 120 * time.Minute
	// SensorExpectedIntervalMin 业务约定的期望上报周期（分钟），与 healthscore 对齐
	SensorExpectedIntervalMin = 5
)

// GetSensorLastDataAt 拉取单台传感器的最近一次上报时间
// 返回 nil 表示从未上报过数据（场景：设备刚部署、传感器被删除后重建等）
func (s *Store) GetSensorLastDataAt(ctx context.Context, sensorID int) (*time.Time, error) {
	var ts *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT MAX(timestamp) FROM sensor_data WHERE sensor_id = $1`, sensorID,
	).Scan(&ts)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

// GetSensorsLastDataAt 批量拉取多台传感器的最近一次上报时间
// 返回 map：sensorID -> *time.Time（值为 nil 表示该传感器从未上报）
//
// 用于存活感知全量扫描：单次 SQL 拿全所有传感器的最后上报时间，
// 避免 N 次 GetSensorLastDataAt 造成的 round-trip 开销。
func (s *Store) GetSensorsLastDataAt(ctx context.Context, sensorIDs []int) (map[int]*time.Time, error) {
	if len(sensorIDs) == 0 {
		return map[int]*time.Time{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT sensor_id, MAX(timestamp) FROM sensor_data
		 WHERE sensor_id = ANY($1)
		 GROUP BY sensor_id`, sensorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]*time.Time, len(sensorIDs))
	for rows.Next() {
		var id int
		var ts *time.Time
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, err
		}
		result[id] = ts
	}
	// 未上报过的传感器在 map 中不存在，调用方按 nil 处理
	return result, rows.Err()
}

// ComputeSensorState 纯函数：根据最近上报时间与当前时间判定在线状态
//
// 状态分档：
//   - 距今 < 10min              -> online
//   - 10min <= 距今 < 30min     -> stale
//   - 距今 >= 30min             -> offline
//   - 从未上报                  -> unknown
//
// 抽取为纯函数便于单元测试覆盖各种边界场景。
func ComputeSensorState(lastDataAt *time.Time, now time.Time) (model.SensorOnlineState, int) {
	if lastDataAt == nil {
		return model.SensorStateUnknown, -1
	}
	mins := int(now.Sub(*lastDataAt).Minutes())
	switch {
	case mins < int(SensorOnlineThreshold.Minutes()):
		return model.SensorStateOnline, mins
	case mins < int(SensorStaleThreshold.Minutes()):
		return model.SensorStateStale, mins
	default:
		return model.SensorStateOffline, mins
	}
}

// GetSensorsWithSections 拉取所有传感器及其所属断面 ID
// 用于存活感知全量扫描的输入数据
type SensorSectionPair struct {
	SensorID  int
	SectionID int
}

func (s *Store) GetSensorsWithSections(ctx context.Context) ([]SensorSectionPair, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, section_id FROM sensors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SensorSectionPair
	for rows.Next() {
		var p SensorSectionPair
		if err := rows.Scan(&p.SensorID, &p.SectionID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ===========================================
// 健康度评分相关查询与写入
// ===========================================

// HistoryPoint 历史曲线的一个时间点
// 与前端 HealthHistoryPoint 字段对齐：avg_score / min_score / max_score / samples
type HistoryPoint struct {
	Bucket   time.Time `json:"bucket"`
	AvgScore float64   `json:"avg_score"`
	MinScore float64   `json:"min_score"`
	MaxScore float64   `json:"max_score"`
	Samples  int       `json:"samples"`
}

// RankItem 健康度排名条目（看板用）
// 字段与前端 SectionHealthRankItem 一一对应
type RankItem struct {
	SectionID                  int     `json:"section_id"`
	SectionCode                string  `json:"section_code"`
	SectionName                string  `json:"section_name"`
	LineCode                   string  `json:"line_code"`
	PositionType               string  `json:"position_type"`
	TotalScore                 float64 `json:"total_score"`
	Grade                      string  `json:"grade"`
	DisplacementScore          float64 `json:"displacement_score"`
	CrackScore                 float64 `json:"crack_score"`
	StrainScore                float64 `json:"strain_score"`
	AlertDimensionScore        float64 `json:"alert_dimension_score"`
	TrendDimensionScore        float64 `json:"trend_dimension_score"`
	StabilityDimensionScore    float64 `json:"stability_dimension_score"`
	CompletenessDimensionScore float64 `json:"completeness_dimension_score"`
	Sensitivity                float64 `json:"sensitivity"`
	TriggerType                string  `json:"trigger_type"`
	CalculatedAt               time.Time `json:"calculated_at"`
	RecentAlertCount           int     `json:"recent_alert_count"`
	PrevScore                  float64 `json:"prev_score"`
	ScoreTrend                 float64 `json:"score_trend"`
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

// GetHealthScoreHistoryAggregated 获取历史评分曲线（按 interval 聚合）
//
// 修复"阶梯状跳变"bug：与 GetHistoricalDataAggregated 同根因——
// 3 参数 time_bucket 对齐到 1970-01-01 00:00:00 UTC + N*interval，
// 与评分实际计算时刻（cron 触发，非整点）对齐错位。改用 4 参数版本，
// 把查询区间 start 作为 origin，保证 bucket 边界与评分时间点对齐。
func (s *Store) GetHealthScoreHistoryAggregated(ctx context.Context, sectionID int, start, end time.Time, interval string) ([]HistoryPoint, error) {
	q := fmt.Sprintf(`
		SELECT time_bucket('%s'::interval, calculated_at, $2) AS bucket,
		       AVG(total_score)::double precision AS avg_score,
		       MIN(total_score)::double precision AS min_score,
		       MAX(total_score)::double precision AS max_score,
		       COUNT(*)::int AS samples
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
		if err := rows.Scan(&p.Bucket, &p.AvgScore, &p.MinScore, &p.MaxScore, &p.Samples); err != nil {
			return nil, err
		}
		pts = append(pts, p)
	}
	return pts, rows.Err()
}

// GetSectionHealthRank 获取断面健康度排名（按线路）
//
// 设计要点：
//   - latest CTE：取每个 section_id 最新一次评分
//   - prev CTE：取每个 section_id 1 小时之前最近一次评分，用于计算趋势
//   - prev_alerts CTE：取每个 section 最近 7 天内告警数（用于关联告警数展示）
//   - 注意：DISTINCT ON 的 ORDER BY 必须以 DISTINCT ON 列开头，
//     "ORDER BY section_id, calculated_at DESC" 即符合
func (s *Store) GetSectionHealthRank(ctx context.Context, lineCode string) ([]RankItem, error) {
	rows, err := s.pool.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (section_id) section_id, total_score, grade,
			       displacement_score, crack_score, strain_score,
			       alert_dimension_score, trend_dimension_score, stability_dimension_score, completeness_dimension_score,
			       position_type, sensitivity, trigger_type, calculated_at
			FROM section_health_scores
			ORDER BY section_id, calculated_at DESC
		),
		prev AS (
			SELECT DISTINCT ON (section_id) section_id, total_score
			FROM section_health_scores
			WHERE calculated_at < NOW() - INTERVAL '1 hour'
			ORDER BY section_id, calculated_at DESC
		),
		prev_alerts AS (
			SELECT section_id, COUNT(*) AS cnt
			FROM alerts
			WHERE triggered_at >= NOW() - INTERVAL '7 days'
			GROUP BY section_id
		)
		SELECT s.id, s.code, s.name, s.line_code,
		       COALESCE(s.position_type, 'mid') AS position_type,
		       l.total_score, l.grade,
		       l.displacement_score, l.crack_score, l.strain_score,
		       l.alert_dimension_score, l.trend_dimension_score, l.stability_dimension_score, l.completeness_dimension_score,
		       l.sensitivity, l.trigger_type, l.calculated_at,
		       COALESCE(pa.cnt, 0) AS recent_alert_count,
		       COALESCE(p.total_score, 0) AS prev_score,
		       (l.total_score - COALESCE(p.total_score, l.total_score)) AS score_trend
		FROM sections s
		JOIN latest l ON l.section_id = s.id
		LEFT JOIN prev p ON p.section_id = s.id
		LEFT JOIN prev_alerts pa ON pa.section_id = s.id
		WHERE s.line_code = $1
		ORDER BY l.total_score ASC`, lineCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RankItem
	for rows.Next() {
		var r RankItem
		if err := rows.Scan(
			&r.SectionID, &r.SectionCode, &r.SectionName, &r.LineCode,
			&r.PositionType,
			&r.TotalScore, &r.Grade,
			&r.DisplacementScore, &r.CrackScore, &r.StrainScore,
			&r.AlertDimensionScore, &r.TrendDimensionScore, &r.StabilityDimensionScore, &r.CompletenessDimensionScore,
			&r.Sensitivity, &r.TriggerType, &r.CalculatedAt,
			&r.RecentAlertCount, &r.PrevScore, &r.ScoreTrend,
		); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
