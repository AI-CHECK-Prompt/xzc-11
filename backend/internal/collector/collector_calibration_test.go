package collector

import (
	"math"
	"testing"
	"time"
	"tunnel-shm/internal/model"
)

// 用例1：复现 bug 报告中的场景
// 裂缝计出厂校准系数 1.05，RS485 原始上报 12.30mm，
// 修复后入库必须是 12.30 * 1.05 = 12.915mm
func TestApplyCalibrationMap_CrackGauge_105(t *testing.T) {
	data := []model.SensorData{
		{SensorID: 7, Value: 12.30, Timestamp: time.Now()},
	}
	calMap := map[int]float64{7: 1.05}

	applyCalibrationMap(data, calMap)

	want := 12.30 * 1.05
	if math.Abs(data[0].Value-want) > 1e-9 {
		t.Errorf("value = %.6f, want %.6f (12.30 * 1.05)", data[0].Value, want)
	}
	// 关键断言：必须明显大于原始值（确认不再按 1.0 入库）
	if math.Abs(data[0].Value-12.30) < 1e-6 {
		t.Errorf("value = %.6f, bug not fixed: data still stored as raw 12.30", data[0].Value)
	}
}

// 用例2：多传感器批量，每台用各自的校准系数
func TestApplyCalibrationMap_MultipleSensors(t *testing.T) {
	data := []model.SensorData{
		{SensorID: 1, Value: 10.0, Timestamp: time.Now()},
		{SensorID: 2, Value: 20.0, Timestamp: time.Now()},
		{SensorID: 3, Value: 30.0, Timestamp: time.Now()},
	}
	calMap := map[int]float64{
		1: 1.00, // 默认
		2: 1.05, // 裂缝计
		3: 0.98, // 应变计出厂偏低
	}

	applyCalibrationMap(data, calMap)

	wants := []float64{10.0, 20.0 * 1.05, 30.0 * 0.98}
	for i, want := range wants {
		if math.Abs(data[i].Value-want) > 1e-9 {
			t.Errorf("data[%d] (sensor %d) = %.6f, want %.6f",
				i, data[i].SensorID, data[i].Value, want)
		}
	}
}

// 用例3：未配置校准的传感器（map 中查不到）必须保持原值
// 这是兜底行为，避免新接入的传感器因配置缺失而崩溃
func TestApplyCalibrationMap_MissingSensorKeepsRaw(t *testing.T) {
	data := []model.SensorData{
		{SensorID: 999, Value: 5.55, Timestamp: time.Now()},
	}
	calMap := map[int]float64{1: 1.05} // 不包含 sensor 999

	applyCalibrationMap(data, calMap)

	if math.Abs(data[0].Value-5.55) > 1e-9 {
		t.Errorf("missing sensor value changed: got %.6f, want 5.55 (must keep raw)", data[0].Value)
	}
}

// 用例4：空 calMap（所有传感器都没配置）整体保持原值
func TestApplyCalibrationMap_EmptyMap(t *testing.T) {
	data := []model.SensorData{
		{SensorID: 1, Value: 1.0, Timestamp: time.Now()},
		{SensorID: 2, Value: 2.0, Timestamp: time.Now()},
	}
	applyCalibrationMap(data, map[int]float64{})

	if data[0].Value != 1.0 || data[1].Value != 2.0 {
		t.Errorf("empty calMap should keep all values; got %v", data)
	}
}

// 用例5：校准系数为 1.0 时应保持原值（默认行为，不引入噪声）
func TestApplyCalibrationMap_UnityCoefficient(t *testing.T) {
	data := []model.SensorData{
		{SensorID: 1, Value: 7.77, Timestamp: time.Now()},
	}
	calMap := map[int]float64{1: 1.0}

	applyCalibrationMap(data, calMap)

	if math.Abs(data[0].Value-7.77) > 1e-9 {
		t.Errorf("cal=1.0 should not change value; got %.6f", data[0].Value)
	}
}

// 用例6：复现场景
// 批量上报混合了位移计（1.02）/裂缝计（1.05）/应变计（0.97），
// 全部按各自系数修正；这覆盖了用户描述的"出厂系数被忽略"的核心场景。
func TestApplyCalibrationMap_ProductionMixedBatch(t *testing.T) {
	now := time.Now()
	data := []model.SensorData{
		{SensorID: 101, Value: 8.50, Timestamp: now},  // 位移
		{SensorID: 102, Value: 0.32, Timestamp: now},  // 裂缝
		{SensorID: 103, Value: 245.0, Timestamp: now}, // 应变
		{SensorID: 104, Value: 12.30, Timestamp: now}, // 裂缝
	}
	calMap := map[int]float64{
		101: 1.02,
		102: 1.05,
		103: 0.97,
		104: 1.05, // 题目中描述的"出厂 1.05 但按 1.0 入库"的裂缝计
	}

	applyCalibrationMap(data, calMap)

	wants := map[int]float64{
		101: 8.50 * 1.02,
		102: 0.32 * 1.05,
		103: 245.0 * 0.97,
		104: 12.30 * 1.05, // 关键：必须不等于 12.30
	}
	for i, d := range data {
		if want, ok := wants[d.SensorID]; ok {
			if math.Abs(d.Value-want) > 1e-9 {
				t.Errorf("data[%d] sensor %d = %.6f, want %.6f",
					i, d.SensorID, d.Value, want)
			}
		}
	}
}
