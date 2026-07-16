package store

import (
	"math"
	"testing"
	"time"
	"tunnel-shm/internal/model"
)

// 构造有序时间序列
func mkSeries(values []float64, start time.Time, interval time.Duration) []model.SensorData {
	out := make([]model.SensorData, len(values))
	for i, v := range values {
		out[i] = model.SensorData{
			ID:        i + 1,
			SensorID:  1,
			Value:     v,
			Timestamp: start.Add(time.Duration(i) * interval),
		}
	}
	return out
}

// 用例1：复现 bug 报告中的"先抬升后回落"场景
// 24h 内：12.3 -> 14.8 (1h 阶跃) -> 12.5 (缓慢回落)
// 旧实现：endpoint = 0.2mm/天，漏报
// 新实现：sliding 窗口 1h 内 (14.8-12.3)/1h*24 = 60.0mm/天，必告警
//   相邻阶跃 (12.3->14.8) 1h 阶跃 = 60.0mm/天
func TestAnalyzeRate_StepThenRevert(t *testing.T) {
	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	// 关键点：t0=0h(12.3), t1=1h(14.8阶跃), t2=24h(12.5回落)
	data := []model.SensorData{
		{ID: 1, SensorID: 1, Value: 12.3, Timestamp: start},
		{ID: 2, SensorID: 1, Value: 14.8, Timestamp: start.Add(1 * time.Hour)},
		{ID: 3, SensorID: 1, Value: 12.5, Timestamp: start.Add(24 * time.Hour)},
	}
	r, err := AnalyzeRateFromData(1, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 端点速率：(12.5-12.3)/24h*24 = 0.2 mm/天（与 bug 报告一致，漏报）
	if math.Abs(r.EndpointRate-0.2) > 1e-6 {
		t.Errorf("endpoint rate = %.6f, want 0.2", r.EndpointRate)
	}

	// 1h 滑动窗口最大速率：(14.8-12.3)/1h*24 = 60.0 mm/天
	if math.Abs(r.MaxSlidingRate-60.0) > 1e-6 {
		t.Errorf("sliding rate = %.6f, want 60.0", r.MaxSlidingRate)
	}

	// 相邻阶跃最大速率：(14.8-12.3)/1h*24 = 60.0 mm/天
	expectedStep := 2.5 / 1.0 * 24.0
	if math.Abs(r.MaxStepRate-expectedStep) > 1e-6 {
		t.Errorf("step rate = %.6f, want %.6f", r.MaxStepRate, expectedStep)
	}

	// 关键断言：取最严值，必须远大于 0.2（修复 bug）
	if math.Abs(r.Rate) < 1.0 {
		t.Errorf("rate = %.6f, want >= 1.0 (must exceed danger threshold to fix bug)", r.Rate)
	}
}

// 用例2：数据点不足应返回错误
func TestAnalyzeRate_NotEnoughData(t *testing.T) {
	_, err := AnalyzeRateFromData(1, []model.SensorData{
		{Value: 1.0, Timestamp: time.Now()},
	})
	if err == nil {
		t.Error("expected error for single data point, got nil")
	}
}

// 用例3：缓慢漂移（无突变）应取端点速率
func TestAnalyzeRate_SlowDrift(t *testing.T) {
	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	// 24h 缓慢线性漂移 12.0 -> 12.4 (0.4mm/天，未超阈值)
	vals := []float64{12.0, 12.05, 12.1, 12.15, 12.2, 12.25, 12.3, 12.35, 12.4}
	data := mkSeries(vals, start, 3*time.Hour)

	r, err := AnalyzeRateFromData(1, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 端点速率 = 0.4mm/天
	if math.Abs(r.EndpointRate-0.4) > 1e-6 {
		t.Errorf("endpoint rate = %.6f, want 0.4", r.EndpointRate)
	}
	// 不应触发 0.5mm/天位移告警
	if math.Abs(r.Rate) >= 0.5 {
		t.Errorf("rate %.6f should NOT trigger displacement warning (0.4 < 0.5)", r.Rate)
	}
}

// 用例4：单点阶跃（数据稀疏）应被相邻点阶跃检测捕获
func TestAnalyzeRate_SinglePointStep(t *testing.T) {
	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	// 在 5min 间隔上做 0.6mm 阶跃 -> 0.6/5min*24h = 172.8 mm/天
	data := []model.SensorData{
		{Value: 5.0, Timestamp: start},
		{Value: 5.6, Timestamp: start.Add(5 * time.Minute)},
		{Value: 5.0, Timestamp: start.Add(10 * time.Minute)},
	}
	r, err := AnalyzeRateFromData(1, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 应识别为 step 来源
	if r.RateSource != model.RateSourceStep {
		t.Errorf("rate source = %s, want step", r.RateSource)
	}
	// 阶跃速率约 172.8 mm/天
	expectedStep := 0.6 / (5.0 / 60.0) * 24.0
	if math.Abs(r.MaxStepRate-expectedStep) > 1e-3 {
		t.Errorf("step rate = %.6f, want %.6f", r.MaxStepRate, expectedStep)
	}
}

// 用例5：原始 bug 场景的回归 - 旧实现漏报，新实现应告警
// 构造 1min 采样率，更贴合生产环境
func TestAnalyzeRate_RegressionBug(t *testing.T) {
	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	// 关键时间点：
	//   t=0h: 12.3 (稳定)
	//   t=1h30m: 12.3 (稳定，1h30m 内小幅变化)
	//   t=1h35m: 14.8 (5min 内阶跃 2.5mm) <- 真正的危险信号
	//   ... 之后 22h 缓慢回落至 12.5
	data := []model.SensorData{
		{Value: 12.3, Timestamp: start},
		{Value: 12.3, Timestamp: start.Add(90 * time.Minute)},
		{Value: 14.8, Timestamp: start.Add(95 * time.Minute)}, // 5min 阶跃
		{Value: 12.5, Timestamp: start.Add(24 * time.Hour)},   // 缓慢回落
	}
	r, err := AnalyzeRateFromData(1, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 旧 bug 行为：端点速率 = 0.2 < 0.5 阈值
	if math.Abs(r.EndpointRate) >= 0.5 {
		t.Errorf("endpoint rate %.6f should be 0.2 to demonstrate original bug", r.EndpointRate)
	}
	// 新实现修复：rate 必须 >= 1.0 危险阈值（无论是 sliding 还是 step 触发）
	if math.Abs(r.Rate) < 1.0 {
		t.Errorf("new rate %.6f must exceed 1.0 danger threshold to fix the bug", r.Rate)
	}
	// 触发源必须是 sliding 或 step（绝不能是 endpoint）
	if r.RateSource == model.RateSourceEndpoint {
		t.Errorf("rate source should NOT be endpoint, got %s (would miss the intermediate spike)", r.RateSource)
	}
}
