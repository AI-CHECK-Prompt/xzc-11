-- 初始化数据库表结构（在TimescaleDB容器启动时自动执行）
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

-- 传感器时序数据表
CREATE TABLE IF NOT EXISTS sensor_data (
    id SERIAL,
    sensor_id INTEGER NOT NULL,
    value DOUBLE PRECISION NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, timestamp)
);

-- 转换为hypertable
SELECT create_hypertable('sensor_data', 'timestamp', if_not_exists => TRUE,
    chunk_time_interval => INTERVAL '7 days');

-- 索引
CREATE INDEX IF NOT EXISTS idx_sensor_data_sensor_id_time
    ON sensor_data (sensor_id, timestamp DESC);

-- 告警表
CREATE TABLE IF NOT EXISTS alerts (
    id SERIAL PRIMARY KEY,
    section_id INTEGER NOT NULL,
    sensor_id INTEGER NOT NULL,
    level VARCHAR(20) NOT NULL,
    -- 告警类型：rate（速率超阈值） / offline（设备离线 / 数据缺失）
    -- 旧表无该列时由 backend 启动时 ALTER 补齐
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

-- 数据保留策略：3年
SELECT add_retention_policy('sensor_data', INTERVAL '3 years', if_not_exists => TRUE);

-- 插入50个模拟监测断面（3号线，里程K1000-K5000）
DO $$
DECLARE
    i INTEGER;
    sec_code VARCHAR;
    sec_name VARCHAR;
BEGIN
    FOR i IN 1..50 LOOP
        sec_code := 'L3-S' || LPAD(i::TEXT, 3, '0');
        sec_name := '3号线-' || (1000 + i * 80)::TEXT || 'm断面';
        
        INSERT INTO sections (code, name, line_code, station_km, description, location_lat, location_lng)
        VALUES (sec_code, sec_name, '3', 1000 + i * 80, '3号线隧道结构监测断面', 30.5 + i * 0.001, 120.0 + i * 0.001)
        ON CONFLICT (code) DO NOTHING;
    END LOOP;
END $$;

-- 为每个断面插入3个传感器（裂缝计、位移计、应变计各一个）
DO $$
DECLARE
    sec RECORD;
BEGIN
    FOR sec IN SELECT id, code FROM sections LOOP
        -- 裂缝计
        INSERT INTO sensors (section_id, code, type, position, calibration)
        VALUES (sec.id, sec.code || '-CRK', 'crack', '拱顶', 1.0)
        ON CONFLICT (code) DO NOTHING;
        
        -- 位移计
        INSERT INTO sensors (section_id, code, type, position, calibration)
        VALUES (sec.id, sec.code || '-DSP', 'displacement', '左侧墙', 1.0)
        ON CONFLICT (code) DO NOTHING;
        
        -- 应变计
        INSERT INTO sensors (section_id, code, type, position, calibration)
        VALUES (sec.id, sec.code || '-STR', 'strain', '右侧墙', 1.0)
        ON CONFLICT (code) DO NOTHING;
    END LOOP;
END $$;

-- ============================================
-- 断面健康度评估（追加 schema，向后兼容）
-- ============================================

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

-- 评分明细表（可解释）
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