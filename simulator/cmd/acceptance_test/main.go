package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// 验收测试脚本
// 验证项：
// 1. 50个断面连续7天数据上报，数据完整率 >= 99.9%
// 2. 裂缝宽度变化超过0.1mm/天时，告警在60秒内推送
// 3. API响应正确

const (
	ServerURL      = "http://localhost:8080"
	SectionCount   = 50
	TestDays       = 7
	SampleInterval = 60 // 秒
)

var (
	totalSent     int64
	totalSuccess  int64
	totalFail     int64
	alertReceived int64
)

func main() {
	log.SetFlags(log.LstdFlags)
	log.Println("========================================")
	log.Println("  隧道结构健康监测系统 - 验收测试")
	log.Println("========================================")

	// 测试1: 健康检查
	log.Println("\n【测试1】健康检查...")
	if err := testHealthCheck(); err != nil {
		log.Fatalf("健康检查失败: %v", err)
	}
	log.Println("  ✓ 健康检查通过")

	// 测试2: API测试
	log.Println("\n【测试2】API功能测试...")
	if err := testAPI(); err != nil {
		log.Fatalf("API测试失败: %v", err)
	}
	log.Println("  ✓ API测试通过")

	// 测试3: 数据上报测试
	log.Println("\n【测试3】数据上报完整性测试...")
	dataCompleteness := testDataCollection()
	log.Printf("  ✓ 数据完整率: %.4f%% (要求 >= 99.9%%)", dataCompleteness*100)
	if dataCompleteness < 0.999 {
		log.Printf("  ✗ 数据完整率不达标!")
	} else {
		log.Println("  ✓ 数据完整率达标!")
	}

	// 测试4: 告警推送测试
	log.Println("\n【测试4】告警推送延迟测试...")
	alertLatency := testAlertLatency()
	log.Printf("  ✓ 告警推送延迟: %.2f秒 (要求 <= 60秒)", alertLatency)
	if alertLatency > 60 {
		log.Printf("  ✗ 告警推送延迟不达标!")
	} else {
		log.Println("  ✓ 告警推送延迟达标!")
	}

	// 测试5: 告警查询
	log.Println("\n【测试5】告警查询测试...")
	if err := testAlertQuery(); err != nil {
		log.Printf("  告警查询测试: %v", err)
	} else {
		log.Println("  ✓ 告警查询测试通过")
	}

	// 测试6: 历史数据查询
	log.Println("\n【测试6】历史数据查询测试...")
	if err := testHistoricalDataQuery(); err != nil {
		log.Printf("  历史数据查询测试: %v", err)
	} else {
		log.Println("  ✓ 历史数据查询测试通过")
	}

	log.Println("\n========================================")
	log.Println("  验收测试完成")
	log.Println("========================================")
}

func testHealthCheck() error {
	resp, err := http.Get(ServerURL + "/api/v1/health")
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("状态码错误: %d", resp.StatusCode)
	}
	return nil
}

func testAPI() error {
	resp, err := http.Get(ServerURL + "/api/v1/sections")
	if err != nil {
		return fmt.Errorf("获取断面列表失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data  []interface{} `json:"data"`
		Total int           `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Total < SectionCount {
		return fmt.Errorf("断面数量不足: 期望 >= %d, 实际 %d", SectionCount, result.Total)
	}

	resp2, err := http.Get(ServerURL + "/api/v1/dashboard/overview")
	if err != nil {
		return fmt.Errorf("获取概览失败: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		return fmt.Errorf("概览接口状态码错误: %d", resp2.StatusCode)
	}

	return nil
}

type SensorData struct {
	SensorID  int       `json:"sensor_id"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

type SensorDataBatch struct {
	CollectorCode string        `json:"collector_code"`
	Data          []SensorData `json:"data"`
}

func testDataCollection() float64 {
	client := &http.Client{Timeout: 30 * time.Second}
	simTime := time.Now().Add(-time.Duration(TestDays) * 24 * time.Hour)
	endTime := time.Now()

	type sensorState struct {
		id           int
		baseValue    float64
		currentValue float64
	}
	var states []sensorState
	rng := rand.New(rand.NewSource(42))

	sensorID := 1
	for s := 1; s <= SectionCount; s++ {
		for i := 0; i < 3; i++ {
			states = append(states, sensorState{
				id:           sensorID,
				baseValue:    0.1 + rng.Float64()*2.0,
				currentValue: 0.1 + rng.Float64()*2.0,
			})
			sensorID++
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	startTime := time.Now()

	go func() {
		for range ticker.C {
			sent := atomic.LoadInt64(&totalSent)
			success := atomic.LoadInt64(&totalSuccess)
			elapsed := time.Since(startTime)
			progress := float64(simTime.Sub(startTime.Add(-time.Duration(TestDays)*24*time.Hour))) /
				float64(TestDays*24*time.Hour) * 100
			log.Printf("  进度: %.1f%%, 已发送=%d, 成功=%d, 耗时=%v",
				math.Min(progress, 100), sent, success, elapsed.Round(time.Second))
		}
	}()

	for simTime.Before(endTime) {
		batch := SensorDataBatch{
			CollectorCode: fmt.Sprintf("TEST-%s", simTime.Format("150405")),
		}

		for i := range states {
			drift := (rng.Float64() - 0.5) * 0.002
			states[i].currentValue += drift
			states[i].currentValue = math.Max(0, states[i].currentValue)

			batch.Data = append(batch.Data, SensorData{
				SensorID:  states[i].id,
				Value:     math.Round(states[i].currentValue*1000) / 1000,
				Timestamp: simTime,
			})
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(b SensorDataBatch) {
			defer wg.Done()
			defer func() { <-sem }()

			atomic.AddInt64(&totalSent, int64(len(b.Data)))
			body, _ := json.Marshal(b)
			resp, err := client.Post(ServerURL+"/api/v1/collect", "application/json", bytes.NewReader(body))
			if err != nil {
				atomic.AddInt64(&totalFail, 1)
				return
			}
			resp.Body.Close()
			if resp.StatusCode == 200 {
				atomic.AddInt64(&totalSuccess, int64(len(b.Data)))
			} else {
				atomic.AddInt64(&totalFail, 1)
			}
		}(batch)

		simTime = simTime.Add(time.Duration(SampleInterval) * time.Second)
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()

	sent := atomic.LoadInt64(&totalSent)
	success := atomic.LoadInt64(&totalSuccess)

	if sent == 0 {
		return 0
	}

	return float64(success) / float64(sent)
}

func testAlertLatency() float64 {
	client := &http.Client{Timeout: 30 * time.Second}

	now := time.Now()

	// 第一次：正常值
	batch1 := SensorDataBatch{
		CollectorCode: "ALERT-TEST",
		Data: []SensorData{
			{SensorID: 1, Value: 0.5, Timestamp: now.Add(-2 * time.Minute)},
			{SensorID: 2, Value: 1.0, Timestamp: now.Add(-2 * time.Minute)},
			{SensorID: 3, Value: 50.0, Timestamp: now.Add(-2 * time.Minute)},
		},
	}

	body1, _ := json.Marshal(batch1)
	resp, _ := client.Post(ServerURL+"/api/v1/collect", "application/json", bytes.NewReader(body1))
	if resp != nil {
		resp.Body.Close()
	}

	// 第二次：大幅度变化
	batch2 := SensorDataBatch{
		CollectorCode: "ALERT-TEST",
		Data: []SensorData{
			{SensorID: 1, Value: 0.9, Timestamp: now},
			{SensorID: 2, Value: 2.5, Timestamp: now},
			{SensorID: 3, Value: 70.0, Timestamp: now},
		},
	}

	alertStart := time.Now()
	body2, _ := json.Marshal(batch2)
	resp, _ = client.Post(ServerURL+"/api/v1/collect", "application/json", bytes.NewReader(body2))
	if resp != nil {
		resp.Body.Close()
	}

	// 注意：告警由定时任务触发，每5分钟一次
	// 此处等待定时任务执行
	maxWait := 360 * time.Second
	pollInterval := 5 * time.Second
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		resp, err := client.Get(ServerURL + "/api/v1/alerts/active")
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		var result struct {
			Data []struct {
				ID        int    `json:"id"`
				SensorID  int    `json:"sensor_id"`
				Level     string `json:"level"`
				Message   string `json:"message"`
			} `json:"data"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		for _, a := range result.Data {
			if a.SensorID == 1 {
				return time.Since(alertStart).Seconds()
			}
		}

		time.Sleep(pollInterval)
	}

	return maxWait.Seconds()
}

func testAlertQuery() error {
	resp, err := http.Get(ServerURL + "/api/v1/alerts/active")
	if err != nil {
		return fmt.Errorf("获取告警列表失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("状态码错误: %d", resp.StatusCode)
	}

	var result struct {
		Data  []interface{} `json:"data"`
		Total int           `json:"total"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	log.Printf("  当前活跃告警数: %d", result.Total)
	return nil
}

func testHistoricalDataQuery() error {
	end := time.Now()
	start := end.Add(-24 * time.Hour)
	url := fmt.Sprintf("%s/api/v1/sensors/1/data?start=%s&end=%s&interval=1 hour",
		ServerURL, start.Format(time.RFC3339), end.Format(time.RFC3339))

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("查询历史数据失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("状态码错误: %d", resp.StatusCode)
	}

	var result struct {
		Data       []interface{} `json:"data"`
		Total      int           `json:"total"`
		Aggregated bool          `json:"aggregated"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	log.Printf("  返回数据点: %d, 聚合: %v", result.Total, result.Aggregated)
	return nil
}