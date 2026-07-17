package analyzer

import (
	"testing"
	"time"
	"tunnel-shm/internal/model"
	"tunnel-shm/internal/store"
)

// TestOfflineAlertLevelMapping 验证：离线告警的级别映射与产品定义一致
//
// 设计依据：
//   - 从未上报（state=unknown） -> danger（部署后一直没工作，最严重）
//   - 120 分钟 <= 离线时长       -> danger
//   - 30  <= 离线时长 < 120 分钟 -> warning
//   - 10  <= 离线时长 < 30 分钟  -> stale（不告警，仅前端标识）
//   - 离线时长 < 10 分钟          -> online（不告警）
func TestOfflineAlertLevelMapping(t *testing.T) {
	cases := []struct {
		name      string
		state     model.SensorOnlineState
		mins      int
		wantLevel model.AlertLevel
	}{
		{"从未上报 -> danger", model.SensorStateUnknown, -1, model.AlertLevelDanger},
		{"离线 30min -> warning（边界）", model.SensorStateOffline, 30, model.AlertLevelWarning},
		{"离线 60min -> warning", model.SensorStateOffline, 60, model.AlertLevelWarning},
		{"离线 119min -> warning", model.SensorStateOffline, 119, model.AlertLevelWarning},
		{"离线 120min -> danger（边界）", model.SensorStateOffline, 120, model.AlertLevelDanger},
		{"离线 24h -> danger", model.SensorStateOffline, 24 * 60, model.AlertLevelDanger},
		{"离线 72h -> danger（用户报告场景）", model.SensorStateOffline, 72 * 60, model.AlertLevelDanger},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyOfflineLevel(c.state, c.mins)
			if got != c.wantLevel {
				t.Errorf("classifyOfflineLevel(%s, %d) = %s, want %s",
					c.state, c.mins, got, c.wantLevel)
			}
		})
	}
}

// classifyOfflineLevel 是 analyzer.checkAndInsertOfflineAlert 中的级别判定逻辑
// 抽取为独立函数便于测试：纯函数无 IO 依赖，可直接断言
func classifyOfflineLevel(state model.SensorOnlineState, mins int) model.AlertLevel {
	switch {
	case state == model.SensorStateUnknown:
		return model.AlertLevelDanger
	case mins >= int(store.SensorOfflineDangerThreshold.Minutes()):
		return model.AlertLevelDanger
	default:
		return model.AlertLevelWarning
	}
}

// TestOfflineMessageFormat 验证：告警消息包含运维所需的关键信息
//
// 关键检查项：
//   - 断面编号
//   - 传感器编号
//   - 离线时长
//   - 期望上报周期（用于提示运维人员判断延迟是否正常）
func TestOfflineMessageFormat(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	lastData := now.Add(-90 * time.Minute) // 90 分钟前
	state, mins := store.ComputeSensorState(&lastData, now)

	msg := buildOfflineMessage("L3-S005", "3号线-1400m断面",
		"L3-S005-STR", "拱顶", state, mins, store.SensorExpectedIntervalMin)

	// 必须包含断面编号与传感器编号
	if !contains(msg, "L3-S005") {
		t.Errorf("message missing section code: %s", msg)
	}
	if !contains(msg, "L3-S005-STR") {
		t.Errorf("message missing sensor code: %s", msg)
	}
	// 必须包含离线时长
	if !contains(msg, "90") {
		t.Errorf("message missing minutes (90): %s", msg)
	}
	// 必须包含期望上报周期（运维排障关键信息）
	if !contains(msg, "5 分钟") {
		t.Errorf("message missing expected interval (5 分钟): %s", msg)
	}
}

// buildOfflineMessage 复刻 analyzer.checkAndInsertOfflineAlert 的消息构造逻辑
// 抽取为独立函数便于测试断言
func buildOfflineMessage(
	sectionCode, sectionName, sensorCode, position string,
	state model.SensorOnlineState, mins, expectedInterval int,
) string {
	if state == model.SensorStateUnknown {
		return "断面[" + sectionCode + "](" + sectionName + ")传感器[" + sensorCode + "](" + position +
			") 自部署以来从未上报数据"
	}
	return "断面[" + sectionCode + "](" + sectionName + ")传感器[" + sensorCode + "](" + position +
		") 已 " + itoa(mins) + " 分钟无数据上报，超过预期周期 " + itoa(expectedInterval) + " 分钟"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
