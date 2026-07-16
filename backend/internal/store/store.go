package store

import (
	"context"
	"fmt"
	"time"
	"tunnel-shm/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
		message TEXT NOT NULL,
		deformation_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
		threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
		status VARCHAR(20) NOT NULL DEFAULT 'active',
		triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		resolved_at TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts (status);
	CREATE INDEX IF NOT EXISTS idx_alerts_section ON alerts (section_id, triggered_at DESC);

	-- 数据保留策略：自动删除3年前的数据
	SELECT add_retention_policy('sensor_data', INTERVAL '3 years', if_not_exists => TRUE);
	`
	_, err := s.pool.Exec(ctx, schema)
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
func (s *Store) GetHistoricalDataAggregated(ctx context.Context, sensorID int, start, end time.Time, interval string) ([]model.SensorData, error) {
	query := fmt.Sprintf(
		`SELECT
			MIN(id) as id,
			sensor_id,
			AVG(value) as value,
			time_bucket('%s', timestamp) as bucket
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

// CalculateDeformationRate 计算变形速率（24小时，mm/天）
func (s *Store) CalculateDeformationRate(ctx context.Context, sensorID int) (*model.DeformationRate, error) {
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	rows, err := s.pool.Query(ctx,
		`SELECT id, sensor_id, value, timestamp
		 FROM sensor_data
		 WHERE sensor_id = $1 AND timestamp >= $2 AND timestamp <= $3
		 ORDER BY timestamp ASC`, sensorID, yesterday, now)
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

	if len(data) < 2 {
		return nil, fmt.Errorf("数据点不足")
	}

	first := data[0]
	last := data[len(data)-1]

	rate := last.Value - first.Value

	result := &model.DeformationRate{
		SensorID:   sensorID,
		Rate:       rate,
		StartTime:  first.Timestamp,
		EndTime:    last.Timestamp,
		DataPoints: len(data),
		LastValue:  last.Value,
		FirstValue: first.Value,
	}

	return result, nil
}

// InsertAlert 插入告警
func (s *Store) InsertAlert(ctx context.Context, alert *model.Alert) error {
	return s.pool.QueryRow(ctx,
		`INSERT INTO alerts (section_id, sensor_id, level, message, deformation_rate, threshold, status, triggered_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		alert.SectionID, alert.SensorID, alert.Level, alert.Message,
		alert.DeformationRate, alert.Threshold, alert.Status, alert.TriggeredAt,
	).Scan(&alert.ID)
}

// GetActiveAlerts 获取活跃告警
func (s *Store) GetActiveAlerts(ctx context.Context) ([]model.Alert, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, section_id, sensor_id, level, message, deformation_rate, threshold, status, triggered_at, resolved_at
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
		if err := rows.Scan(&a.ID, &a.SectionID, &a.SensorID, &a.Level, &a.Message,
			&a.DeformationRate, &a.Threshold, &a.Status, &a.TriggeredAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// GetSectionAlerts 获取断面告警
func (s *Store) GetSectionAlerts(ctx context.Context, sectionID int, limit int) ([]model.Alert, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, section_id, sensor_id, level, message, deformation_rate, threshold, status, triggered_at, resolved_at
		 FROM alerts
		 WHERE section_id = $1
		 ORDER BY triggered_at DESC
		 LIMIT $2`, sectionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.SectionID, &a.SensorID, &a.Level, &a.Message,
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

// GetSections 获取所有断面
func (s *Store) GetSections(ctx context.Context) ([]model.Section, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, code, name, line_code, station_km, description, location_lat, location_lng
		 FROM sections ORDER BY station_km ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sections []model.Section
	for rows.Next() {
		var sec model.Section
		if err := rows.Scan(&sec.ID, &sec.Code, &sec.Name, &sec.LineCode,
			&sec.StationKm, &sec.Description, &sec.LocationLat, &sec.LocationLng); err != nil {
			return nil, err
		}
		sections = append(sections, sec)
	}
	return sections, rows.Err()
}

// GetSection 获取单个断面
func (s *Store) GetSection(ctx context.Context, id int) (*model.Section, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, code, name, line_code, station_km, description, location_lat, location_lng
		 FROM sections WHERE id = $1`, id)

	var sec model.Section
	err := row.Scan(&sec.ID, &sec.Code, &sec.Name, &sec.LineCode,
		&sec.StationKm, &sec.Description, &sec.LocationLat, &sec.LocationLng)
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
func (s *Store) CheckRecentAlert(ctx context.Context, sensorID int, level model.AlertLevel, withinMinutes int) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM alerts
		 WHERE sensor_id = $1 AND level = $2 AND status = 'active'
		 AND triggered_at > NOW() - INTERVAL '1 minute' * $3`, sensorID, level, withinMinutes).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}