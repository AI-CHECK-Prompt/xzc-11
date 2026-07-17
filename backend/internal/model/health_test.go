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
	if PositionSensitivity[PositionShaft] <= PositionSensitivity[PositionMid] {
		t.Error("风井区域敏感度应高于区间中部")
	}
}

func TestSensorTypeWeight(t *testing.T) {
	total := SensorTypeWeight[SensorTypeDisplacement] +
		SensorTypeWeight[SensorTypeCrack] +
		SensorTypeWeight[SensorTypeStrain]
	if total < 0.99 || total > 1.01 {
		t.Errorf("传感器类型权重之和应为 1.0，实际=%v", total)
	}
}

func TestDimensionWeights(t *testing.T) {
	total := DimensionWeights.Alert + DimensionWeights.Trend +
		DimensionWeights.Stability + DimensionWeights.Completeness
	if total < 0.99 || total > 1.01 {
		t.Errorf("维度权重之和应为 1.0，实际=%v", total)
	}
}
