package healthscore

import (
	"context"
	"testing"

	"tunnel-shm/internal/model"
)

func TestComputeSectionScore_AllExcellent(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{
		makeSensor(11, 1, model.SensorTypeDisplacement),
		makeSensor(12, 1, model.SensorTypeCrack),
		makeSensor(13, 1, model.SensorTypeStrain),
	}
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 0.01, Rate7d: 0.005, Rate30d: 0.005, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.01},
		{SensorID: 12, SensorType: model.SensorTypeCrack, Rate24h: 0.001, Rate7d: 0.0005, Rate30d: 0.0005, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.001},
		{SensorID: 13, SensorType: model.SensorTypeStrain, Rate24h: 0.5, Rate7d: 0.3, Rate30d: 0.3, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.1},
	}
	score, _, _, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if score.Grade != model.HealthGradeExcellent {
		t.Errorf("全优场景应得优良，实际=%v 分=%.2f", score.Grade, score.TotalScore)
	}
	if score.TotalScore < 90 {
		t.Errorf("全优场景分数应≥90，实际=%.2f", score.TotalScore)
	}
}

func TestComputeSectionScore_ContinuousAlerts(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}
	// 3 次连续告警 + 高速率
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 1.2, Rate7d: 0.8, Rate30d: 0.3, RecentAlertCount: 3, DataCompleteness: 1.0, HistoricalVariance: 0.5},
	}
	score, _, _, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err != nil {
		t.Fatal(err)
	}
	// 评分应明显下降（< 60，至少到关注以下）
	if score.TotalScore >= 60 {
		t.Errorf("连续3次告警后分应<60，实际=%.2f, grade=%v", score.TotalScore, score.Grade)
	}
}

func TestComputeSectionScore_PositionSensitivity(t *testing.T) {
	// 同样输入下，联络通道分应低于区间中部
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 0.3, Rate7d: 0.2, Rate30d: 0.1, RecentAlertCount: 0, DataCompleteness: 1.0, HistoricalVariance: 0.1},
	}
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}

	midSec := makeSection(1, model.PositionMid)
	crossSec := makeSection(1, model.PositionCross)

	midScore, _, _, _ := ComputeSectionScore(context.Background(), midSec, sensors, inputs)
	crossScore, _, _, _ := ComputeSectionScore(context.Background(), crossSec, sensors, inputs)

	if crossScore.TotalScore >= midScore.TotalScore {
		t.Errorf("联络通道分应低于区间中部，mid=%.2f, cross=%.2f", midScore.TotalScore, crossScore.TotalScore)
	}
}

func TestComputeSectionScore_DetailsExplainable(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}
	inputs := []SensorScoreInput{
		{SensorID: 11, SensorType: model.SensorTypeDisplacement, Rate24h: 0.6, Rate7d: 0.4, Rate30d: 0.2, RecentAlertCount: 1, DataCompleteness: 1.0, HistoricalVariance: 0.2},
	}
	_, details, intermediates, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 4 {
		t.Errorf("应有4个维度明细，实际=%d", len(details))
	}
	if len(intermediates) != 1 {
		t.Errorf("应有1条中间数据，实际=%d", len(intermediates))
	}
	for _, d := range details {
		if d.Explanation == "" {
			t.Errorf("明细 %s/%s 缺少解释", d.Dimension, d.SubDimension)
		}
	}
}

func TestComputeSectionScore_LengthMismatch(t *testing.T) {
	sec := makeSection(1, model.PositionMid)
	sensors := []model.Sensor{makeSensor(11, 1, model.SensorTypeDisplacement)}
	inputs := []SensorScoreInput{} // 空
	_, _, _, err := ComputeSectionScore(context.Background(), sec, sensors, inputs)
	if err == nil {
		t.Error("传感器与输入不匹配应返回错误")
	}
}
