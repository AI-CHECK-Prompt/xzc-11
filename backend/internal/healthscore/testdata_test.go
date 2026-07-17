package healthscore

import (
	"time"

	"tunnel-shm/internal/model"
)

// makeSensor 构造测试用传感器
func makeSensor(id int, sectionID int, t model.SensorType) model.Sensor {
	return model.Sensor{
		ID: id, SectionID: sectionID, Code: "S-T", Type: t, Calibration: 1.0,
	}
}

// makeSection 构造测试用断面
func makeSection(id int, posType model.SectionPositionType) model.Section {
	return model.Section{
		ID: id, Code: "L3-S001", Name: "test", LineCode: "3",
		StationKm: 1000, PositionType: posType,
	}
}

// now 返回当前时间（用于速率计算）
func now() time.Time { return time.Now() }
