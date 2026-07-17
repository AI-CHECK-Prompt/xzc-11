package analyzer

import (
	"testing"
	"tunnel-shm/internal/model"
)

// TestExceedsWarningThreshold 验证：自动恢复判定逻辑与告警触发逻辑保持完全一致
//
// 设计依据：
//   - analyzer.analyzeSensor 中，absRate >= warningThreshold 即触发告警
//   - 自动恢复条件：absRate < warningThreshold（即 !exceedsWarningThreshold）
//   - 因此本函数的返回值必须与告警触发条件一一对应
func TestExceedsWarningThreshold(t *testing.T) {
	th := DefaultThreshold

	cases := []struct {
		name      string
		sensorType model.SensorType
		absRate   float64
		want      bool
	}{
		// 裂缝计：默认 warning 阈值 0.1 mm/天
		{"crack低于阈值", model.SensorTypeCrack, 0.05, false},
		{"crack恰好等于阈值", model.SensorTypeCrack, 0.1, true},
		{"crack刚好超过阈值", model.SensorTypeCrack, 0.1001, true},
		{"crack远超阈值", model.SensorTypeCrack, 0.5, true},

		// 位移计：默认 warning 阈值 0.5 mm/天
		{"displacement低于阈值", model.SensorTypeDisplacement, 0.3, false},
		{"displacement恰好等于阈值", model.SensorTypeDisplacement, 0.5, true},
		{"displacement刚好超过阈值", model.SensorTypeDisplacement, 0.5001, true},
		{"displacement远超阈值", model.SensorTypeDisplacement, 2.0, true},

		// 应变计：默认 warning 阈值 10 με/天
		{"strain低于阈值", model.SensorTypeStrain, 5, false},
		{"strain恰好等于阈值", model.SensorTypeStrain, 10, true},
		{"strain刚好超过阈值", model.SensorTypeStrain, 10.001, true},
		{"strain远超阈值", model.SensorTypeStrain, 50, true},

		// 未知类型：保守返回 false（不判定为恢复）
		{"未知类型不恢复", model.SensorType("unknown"), 100, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := exceedsWarningThreshold(c.sensorType, c.absRate, th)
			if got != c.want {
				t.Errorf("exceedsWarningThreshold(%s, %v) = %v, want %v",
					c.sensorType, c.absRate, got, c.want)
			}
		})
	}
}

// TestExceedsWarningThreshold_CustomThreshold 验证：自定义阈值被正确读取
//
// 防止后续运维人员通过环境变量/配置文件注入自定义阈值时，
// 自动恢复逻辑仍按默认阈值判定导致"永远不恢复"或"误恢复"。
func TestExceedsWarningThreshold_CustomThreshold(t *testing.T) {
	th := Threshold{
		CrackRateWarning:          0.5,  // 放宽裂缝阈值
		DisplacementRateWarning:   2.0,  // 放宽位移阈值
		StrainRateWarning:         50.0, // 放宽应变阈值
	}

	// 0.3 mm/天：默认 0.1 下会告警；放宽到 0.5 后不再告警
	if exceedsWarningThreshold(model.SensorTypeCrack, 0.3, th) {
		t.Error("自定义阈值下，0.3 不应触发告警")
	}
	// 0.6 mm/天：放宽后仍超过 0.5，需告警
	if !exceedsWarningThreshold(model.SensorTypeCrack, 0.6, th) {
		t.Error("自定义阈值下，0.6 应触发告警")
	}
}

// TestExceedsWarningThreshold_RecoversOnlyBelowThreshold 验证：恢复判定边界
//
// 关键不变量：与告警触发逻辑严格对称——
//   - absRate = warningThreshold  => 视为"仍处于告警状态"，不恢复
//   - absRate  < warningThreshold  => 视为"已恢复"
//
// 这是用户报告中"warning 级告警 2 个月未关闭"问题的核心修复点。
func TestExceedsWarningThreshold_RecoversOnlyBelowThreshold(t *testing.T) {
	th := DefaultThreshold

	// 边界值：恰好等于阈值 → 不应恢复（仍告警）
	if !exceedsWarningThreshold(model.SensorTypeCrack, th.CrackRateWarning, th) {
		t.Error("absRate == warningThreshold 应当仍告警，不应自动恢复")
	}
	// 比阈值小一个最小精度 → 视为恢复
	if exceedsWarningThreshold(model.SensorTypeCrack, th.CrackRateWarning*0.99, th) {
		t.Error("absRate < warningThreshold 应当判定为已恢复")
	}
}
