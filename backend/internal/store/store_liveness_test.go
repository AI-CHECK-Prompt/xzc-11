package store

import (
	"testing"
	"time"
	"tunnel-shm/internal/model"
)

// TestComputeSensorState_Online 验证：刚上报的数据应判定为 online
//
// 对应生产场景：传感器按 5 分钟周期稳定上报，无任何延迟
func TestComputeSensorState_Online(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	lastData := now.Add(-3 * time.Minute) // 3 分钟前

	state, mins := ComputeSensorState(&lastData, now)
	if state != model.SensorStateOnline {
		t.Errorf("state = %s, want online", state)
	}
	if mins != 3 {
		t.Errorf("minutes = %d, want 3", mins)
	}
}

// TestComputeSensorState_Stale 验证：10~30 分钟无数据应判定为 stale（亚健康）
//
// 对应生产场景：单次网络瞬抖或采集器重启
func TestComputeSensorState_Stale(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		ago      time.Duration
		wantMins int
	}{
		{"stale刚到10min", 10 * time.Minute, 10},
		{"stale中间15min", 15 * time.Minute, 15},
		{"stale接近29min", 29 * time.Minute, 29},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lastData := now.Add(-c.ago)
			state, mins := ComputeSensorState(&lastData, now)
			if state != model.SensorStateStale {
				t.Errorf("state = %s, want stale", state)
			}
			if mins != c.wantMins {
				t.Errorf("minutes = %d, want %d", mins, c.wantMins)
			}
		})
	}
}

// TestComputeSensorState_Offline 验证：>= 30 分钟无数据应判定为 offline
//
// 核心修复场景：用户报告的"72 小时离线"必须能正确识别。
// 30min -> 60min -> 120min -> 4320min（72h） 应都判定为 offline
func TestComputeSensorState_Offline(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ago  time.Duration
	}{
		{"offline刚到30min", 30 * time.Minute},
		{"offline中间60min", 60 * time.Minute},
		{"offline 119min", 119 * time.Minute},
		{"offline 120min (升级danger阈值)", 120 * time.Minute},
		{"offline 24h", 24 * time.Hour},
		{"offline 72h (用户报告场景)", 72 * time.Hour},
		{"offline 30d", 30 * 24 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lastData := now.Add(-c.ago)
			state, _ := ComputeSensorState(&lastData, now)
			if state != model.SensorStateOffline {
				t.Errorf("state = %s, want offline (ago=%v)", state, c.ago)
			}
		})
	}
}

// TestComputeSensorState_Unknown 验证：从未上报数据的传感器应判定为 unknown
//
// 对应生产场景：刚部署的传感器、传感器被删除后重建、采集器第一次启动前
func TestComputeSensorState_Unknown(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	state, mins := ComputeSensorState(nil, now)
	if state != model.SensorStateUnknown {
		t.Errorf("state = %s, want unknown", state)
	}
	if mins != -1 {
		t.Errorf("minutes = %d, want -1", mins)
	}
}

// TestComputeSensorState_ThresholdBoundary 验证：阈值边界值正确分档
//
// 关键边界：
//   - 9 min 59 s  -> online（差 1 秒到 stale 边界）
//   - 10 min      -> stale（恰好等于 online 阈值，应进入下一档）
//   - 29 min 59 s -> stale
//   - 30 min      -> offline
func TestComputeSensorState_ThresholdBoundary(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		ago     time.Duration
		want    model.SensorOnlineState
	}{
		{"9分59秒(差1秒到阈值)", 9*time.Minute + 59*time.Second, model.SensorStateOnline},
		{"10分(恰好进入stale)", 10 * time.Minute, model.SensorStateStale},
		{"29分59秒(差1秒到offline)", 29*time.Minute + 59*time.Second, model.SensorStateStale},
		{"30分(恰好进入offline)", 30 * time.Minute, model.SensorStateOffline},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lastData := now.Add(-c.ago)
			state, _ := ComputeSensorState(&lastData, now)
			if state != c.want {
				t.Errorf("ago=%v: state = %s, want %s", c.ago, state, c.want)
			}
		})
	}
}

// TestOfflineThresholds 验证：包级阈值常量与设计预期一致
//
// 防止后续误改阈值导致告警行为与产品定义不符：
//   - online 上限 10 min
//   - stale 上限 30 min
//   - offline warning 阈值 30 min（与 stale 上限一致）
//   - offline danger 阈值 120 min
func TestOfflineThresholds(t *testing.T) {
	if SensorOnlineThreshold != 10*time.Minute {
		t.Errorf("SensorOnlineThreshold = %v, want 10m", SensorOnlineThreshold)
	}
	if SensorStaleThreshold != 30*time.Minute {
		t.Errorf("SensorStaleThreshold = %v, want 30m", SensorStaleThreshold)
	}
	if SensorOfflineWarningThreshold != 30*time.Minute {
		t.Errorf("SensorOfflineWarningThreshold = %v, want 30m", SensorOfflineWarningThreshold)
	}
	if SensorOfflineDangerThreshold != 120*time.Minute {
		t.Errorf("SensorOfflineDangerThreshold = %v, want 120m", SensorOfflineDangerThreshold)
	}
	// 关键不变量：danger > warning（保证告警能正常升级）
	if SensorOfflineDangerThreshold <= SensorOfflineWarningThreshold {
		t.Error("danger 阈值必须大于 warning 阈值")
	}
}
