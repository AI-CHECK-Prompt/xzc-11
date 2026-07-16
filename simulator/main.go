package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// SensorData 传感器数据
type SensorData struct {
	SensorID  int       `json:"sensor_id"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// SensorDataBatch 批量数据
type SensorDataBatch struct {
	CollectorCode string        `json:"collector_code"`
	Data          []SensorData `json:"data"`
}

// SimulatorConfig 模拟器配置
type SimulatorConfig struct {
	ServerURL   string
	Sections    int
	Days        int
	Interval    time.Duration
	FaultRate   float64 // 故障率（模拟数据丢失）
}

// SensorState 传感器状态
type SensorState struct {
	SensorID    int
	Type        string
	BaseValue   float64  // 基准值
	DriftRate   float64  // 漂移速率（mm/天）
	NoiseStd    float64  // 噪声标准差
	CurrentValue float64
}

var (
	totalSent    int64
	totalSuccess int64
	totalFail    int64
)

func main() {
	serverURL := flag.String("url", "http://localhost:8080/api/v1/collect", "采集服务URL")
	sections := flag.Int("sections", 50, "模拟断面数量")
	days := flag.Int("days", 7, "模拟天数")
	interval := flag.Int("interval", 60, "采样间隔(秒)")
	faultRate := flag.Float64("fault", 0.001, "故障率(0-1)")
	flag.Parse()

	config := SimulatorConfig{
		ServerURL: *serverURL,
		Sections:  *sections,
		Days:      *days,
		Interval:  time.Duration(*interval) * time.Second,
		FaultRate: *faultRate,
	}

	log.Printf("【模拟器-启动】配置: 断面=%d, 天数=%d, 间隔=%ds, 故障率=%.2f%%",
		config.Sections, config.Days, *interval, config.FaultRate*100)

	// 初始化传感器状态
	states := initSensorStates(config.Sections)
	totalSensors := len(states)
	log.Printf("【模拟器-初始化】共 %d 个传感器", totalSensors)

	// 计算总数据点数
	totalPoints := config.Days * 24 * 60 * 60 / *interval * totalSensors
	log.Printf("【模拟器-预估】预计产生 %d 条数据", totalPoints)

	// 开始模拟
	startTime := time.Now()
	simEndTime := time.Now().Add(time.Duration(config.Days) * 24 * time.Hour)

	// 从7天前开始模拟
	simTime := time.Now().Add(-time.Duration(config.Days) * 24 * time.Hour)

	// 进度统计
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			sent := atomic.LoadInt64(&totalSent)
			success := atomic.LoadInt64(&totalSuccess)
			fail := atomic.LoadInt64(&totalFail)
			elapsed := time.Since(startTime)
			progress := float64(simTime.Sub(startTime.Add(-time.Duration(config.Days)*24*time.Hour))) /
				float64(config.Days*24*time.Hour) * 100

			log.Printf("【模拟器-进度】%.1f%%, 已发送=%d, 成功=%d, 失败=%d, 耗时=%v",
				progress, sent, success, fail, elapsed.Round(time.Second))
		}
	}()

	// 模拟数据上报
	client := &http.Client{Timeout: 30 * time.Second}
	var wg sync.WaitGroup

	// 使用并发发送
	sem := make(chan struct{}, 10) // 最多10个并发

	for simTime.Before(simEndTime) {
		batch := generateBatch(states, simTime, config.FaultRate)
		if len(batch.Data) == 0 {
			simTime = simTime.Add(config.Interval)
			continue
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(b SensorDataBatch, t time.Time) {
			defer wg.Done()
			defer func() { <-sem }()

			atomic.AddInt64(&totalSent, int64(len(b.Data)))
			if sendBatch(client, config.ServerURL, b) {
				atomic.AddInt64(&totalSuccess, int64(len(b.Data)))
			} else {
				atomic.AddInt64(&totalFail, int64(len(b.Data)))
			}
		}(batch, simTime)

		simTime = simTime.Add(config.Interval)

		// 控制发送速率，模拟真实上报
		time.Sleep(50 * time.Millisecond)
	}

	wg.Wait()

	elapsed := time.Since(startTime)
	sent := atomic.LoadInt64(&totalSent)
	success := atomic.LoadInt64(&totalSuccess)
	fail := atomic.LoadInt64(&totalFail)

	log.Printf("【模拟器-完成】总耗时=%v, 发送=%d, 成功=%d, 失败=%d, 完整率=%.2f%%",
		elapsed.Round(time.Second), sent, success, fail,
		float64(success)/float64(sent)*100)
}

// initSensorStates 初始化传感器状态
func initSensorStates(sectionCount int) []*SensorState {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var states []*SensorState

	sensorID := 1
	for s := 1; s <= sectionCount; s++ {
		// 裂缝计：初始值 0.1~2.0mm
		states = append(states, &SensorState{
			SensorID:     sensorID,
			Type:         "crack",
			BaseValue:    0.1 + rng.Float64()*1.9,
			DriftRate:    (rng.Float64() - 0.5) * 0.2, // -0.1 ~ +0.1 mm/天
			NoiseStd:     0.01,
			CurrentValue: 0,
		})
		sensorID++

		// 位移计：初始值 0~5mm
		states = append(states, &SensorState{
			SensorID:     sensorID,
			Type:         "displacement",
			BaseValue:    rng.Float64() * 5.0,
			DriftRate:    (rng.Float64() - 0.5) * 0.3,
			NoiseStd:     0.02,
			CurrentValue: 0,
		})
		sensorID++

		// 应变计：初始值 0~100με
		states = append(states, &SensorState{
			SensorID:     sensorID,
			Type:         "strain",
			BaseValue:    rng.Float64() * 100,
			DriftRate:    (rng.Float64() - 0.5) * 5.0,
			NoiseStd:     0.5,
			CurrentValue: 0,
		})
		sensorID++
	}

	// 初始化当前值
	for _, st := range states {
		st.CurrentValue = st.BaseValue
	}

	return states
}

// generateBatch 生成一批数据
func generateBatch(states []*SensorState, simTime time.Time, faultRate float64) SensorDataBatch {
	rng := rand.New(rand.NewSource(simTime.UnixNano()))
	batch := SensorDataBatch{
		CollectorCode: fmt.Sprintf("COL-%s", simTime.Format("20060102-150405")),
	}

	for _, st := range states {
		// 模拟故障丢失
		if rng.Float64() < faultRate {
			continue
		}

		// 更新传感器值（漂移 + 噪声）
		driftPerInterval := st.DriftRate / (24 * 60 * 60) * 60 // 每分钟的漂移量
		noise := rng.NormFloat64() * st.NoiseStd
		st.CurrentValue += driftPerInterval + noise

		// 确保值不为负
		st.CurrentValue = math.Max(0, st.CurrentValue)

		batch.Data = append(batch.Data, SensorData{
			SensorID:  st.SensorID,
			Value:     math.Round(st.CurrentValue*1000) / 1000,
			Timestamp: simTime,
		})
	}

	return batch
}

// sendBatch 发送数据到服务器
func sendBatch(client *http.Client, url string, batch SensorDataBatch) bool {
	body, err := json.Marshal(batch)
	if err != nil {
		log.Printf("【模拟器-错误】序列化失败: %v", err)
		return false
	}

	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("【模拟器-错误】发送失败: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("【模拟器-错误】服务器返回: %d", resp.StatusCode)
		return false
	}

	return true
}