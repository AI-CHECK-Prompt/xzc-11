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
    message TEXT NOT NULL,
    deformation_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts (status);
CREATE INDEX IF NOT EXISTS idx_alerts_section ON alerts (section_id, triggered_at DESC);

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