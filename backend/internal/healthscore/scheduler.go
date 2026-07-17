package healthscore

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"tunnel-shm/internal/model"
	"tunnel-shm/internal/store"

	"github.com/robfig/cron/v3"
)

// Scheduler 评分调度器
//
// 职责：
//   - 每 1 分钟 cron 全量重算所有断面
//   - 提供 EnqueueRecompute(sectionID) 给告警分析器调用，实现事件触发
//   - 节流：同一断面 30s 内不重复重算
type Scheduler struct {
	store *store.Store
	cron  *cron.Cron

	mu       sync.Mutex
	throttle map[int]time.Time
	throttleDur time.Duration

	pendingQueue chan int
	wg           sync.WaitGroup
	stopCh       chan struct{}
}

// NewScheduler 构造调度器
func NewScheduler(st *store.Store) *Scheduler {
	return &Scheduler{
		store:        st,
		cron:         cron.New(),
		throttle:     make(map[int]time.Time),
		throttleDur:  30 * time.Second,
		pendingQueue: make(chan int, 256),
		stopCh:       make(chan struct{}),
	}
}

// Start 启动 cron 与工作协程
func (s *Scheduler) Start() {
	// 每 1 分钟全量重算（满足 1 分钟更新 SLA）
	_, err := s.cron.AddFunc("*/1 * * * *", func() {
		s.recomputeAll(context.Background())
	})
	if err != nil {
		log.Printf("【健康度-错误】cron 注册失败: %v", err)
	}
	s.cron.Start()
	log.Println("【健康度-调度】已启动（每 1 分钟全量）")

	// 单 worker 处理事件触发的重算（避免并发写库）
	s.wg.Add(1)
	go s.worker()
}

// Stop 停止
func (s *Scheduler) Stop() {
	close(s.stopCh)
	s.cron.Stop()
	s.wg.Wait()
}

// EnqueueRecompute 事件触发：把 sectionID 放入队列（带节流）
func (s *Scheduler) EnqueueRecompute(sectionID int) {
	s.mu.Lock()
	if last, ok := s.throttle[sectionID]; ok {
		if time.Since(last) < s.throttleDur {
			s.mu.Unlock()
			return
		}
	}
	s.throttle[sectionID] = time.Now()
	s.mu.Unlock()

	select {
	case s.pendingQueue <- sectionID:
	default:
		// 队列满则丢弃，cron 兜底
		log.Printf("【健康度-节流】队列已满，丢弃断面 %d 的事件触发", sectionID)
	}
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case id := <-s.pendingQueue:
			s.recomputeOne(context.Background(), id, model.HealthTriggerEvent)
		}
	}
}

func (s *Scheduler) recomputeAll(ctx context.Context) {
	log.Println("【健康度-调度】开始全量评分...")
	start := time.Now()
	sections, err := s.store.GetSections(ctx)
	if err != nil {
		log.Printf("【健康度-错误】获取断面列表失败: %v", err)
		return
	}
	for _, sec := range sections {
		s.recomputeOne(ctx, sec.ID, model.HealthTriggerCron)
	}
	log.Printf("【健康度-调度】全量评分完成 断面数=%d 耗时=%v", len(sections), time.Since(start))
}

// recomputeOne 算一个断面。trigger 用于写入 DB
func (s *Scheduler) recomputeOne(ctx context.Context, sectionID int, trigger model.HealthTriggerType) {
	// 重新拉取断面（拿到最新的 position_type）
	sec, err := s.store.GetSection(ctx, sectionID)
	if err != nil {
		log.Printf("【健康度-错误】断面 %d 不存在: %v", sectionID, err)
		return
	}
	sensors, err := s.store.GetSensorsBySection(ctx, sectionID)
	if err != nil || len(sensors) == 0 {
		return
	}

	now := time.Now()
	inputs := make([]SensorScoreInput, 0, len(sensors))
	for _, sensor := range sensors {
		// 24h 速率（复用现有算法）
		rate24, err := s.store.CalculateDeformationRate(ctx, sensor.ID)
		var rate24Val float64
		if err == nil {
			rate24Val = rate24.Rate
		}

		// 7d 速率：直接基于原始数据端点
		rate7Val := s.computeEndpointRate(ctx, sensor.ID, now.Add(-7*24*time.Hour), now)
		// 30d 速率
		rate30Val := s.computeEndpointRate(ctx, sensor.ID, now.Add(-30*24*time.Hour), now)
		// 30d 窗口内方差
		variance := s.computeVariance(ctx, sensor.ID, now.Add(-30*24*time.Hour), now)
		// 7d 告警计数
		alertCnt, _ := s.store.GetSectionAlertsSince(ctx, sectionID, now.Add(-7*24*time.Hour))
		// 7d 数据完整度（按每 5 分钟一个点，期望 7*24*12=2016）
		dataCnt, _ := s.store.CountSensorData(ctx, sensor.ID, now.Add(-7*24*time.Hour))
		expected := float64(7 * 24 * 12)
		completeness := math.Min(1.0, float64(dataCnt)/expected)

		inputs = append(inputs, SensorScoreInput{
			SensorID:           sensor.ID,
			SensorType:         sensor.Type,
			Rate24h:            rate24Val,
			Rate7d:             rate7Val,
			Rate30d:            rate30Val,
			RecentAlertCount:   alertCnt,
			DataCompleteness:   completeness,
			HistoricalVariance: variance,
		})
	}

	score, details, inters, err := ComputeSectionScore(ctx, *sec, sensors, inputs)
	if err != nil {
		log.Printf("【健康度-错误】断面 %d 评分失败: %v", sectionID, err)
		return
	}
	score.TriggerType = trigger

	scoreID, err := s.store.InsertHealthScore(ctx, score, details, inters)
	if err != nil {
		log.Printf("【健康度-错误】断面 %d 评分入库失败: %v", sectionID, err)
		return
	}
	log.Printf("【健康度-评分】断面[%d] %s 评分=%.2f 等级=%s 触发=%s scoreID=%d",
		sectionID, sec.Code, score.TotalScore, score.Grade, trigger, scoreID)
}

// computeEndpointRate 端点速率（mm/天）；数据不足返回 0
func (s *Scheduler) computeEndpointRate(ctx context.Context, sensorID int, start, end time.Time) float64 {
	data, err := s.store.GetSensorDataRange(ctx, sensorID, start, end)
	if err != nil || len(data) < 2 {
		return 0
	}
	first, last := data[0], data[len(data)-1]
	hours := last.Timestamp.Sub(first.Timestamp).Hours()
	if hours <= 0 {
		return 0
	}
	return (last.Value - first.Value) / hours * 24.0
}

// computeVariance 区间数据方差
func (s *Scheduler) computeVariance(ctx context.Context, sensorID int, start, end time.Time) float64 {
	data, err := s.store.GetSensorDataRange(ctx, sensorID, start, end)
	if err != nil || len(data) < 2 {
		return 0
	}
	var sum, sum2 float64
	for _, d := range data {
		sum += d.Value
		sum2 += d.Value * d.Value
	}
	n := float64(len(data))
	mean := sum / n
	v := sum2/n - mean*mean
	if v < 0 {
		v = 0
	}
	return math.Sqrt(v)
}
