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
//   - 每 5 分钟 cron 全量重算所有断面（默认周期；运维可调）
//   - 提供 EnqueueRecompute(sectionID) 给告警分析器调用，实现事件触发
//   - 节流：同一断面 30s 内不重复重算
//   - 单飞：避免 cron 触发与上一轮评分重叠并发，避免连接池被打爆
//   - 断面级并发上限：限制同时计算的断面数，避免评分任务抢占采集写入连接
type Scheduler struct {
	store *store.Store
	cron  *cron.Cron

	mu       sync.Mutex
	throttle map[int]time.Time
	throttleDur time.Duration

	// 单飞：防止上轮评分未结束时新一轮 cron 又触发，导致连接池倍增抢占
	runningMu   sync.Mutex
	running     bool

	pendingQueue chan int
	wg           sync.WaitGroup
	stopCh       chan struct{}
}

// 评分并发与超时参数（包级常量，便于测试和调整）
const (
	// RecomputeMaxConcurrency 全量重算时同时计算的断面数上限。
	// 数值越大评分总耗时越短，但同一时刻对数据库的并发查询也越多；
	// 经验值 4：单轮查询量 ~ 4 * 每断面查询数 ≈ 32 并发连接，
	// 在 80 连接池下留出 48 给采集写入 / API 查询，避免写入被挤兑。
	RecomputeMaxConcurrency = 4
	// RecomputePerSectionTimeout 单断面评分硬超时。
	// 防止某断面因慢查询拖垮整轮；超时后跳过该断面继续后续断面。
	RecomputePerSectionTimeout = 60 * time.Second
)

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
	// 每 5 分钟全量重算（默认周期）。
	// 评分属于"准实时"指标，5 分钟足够让运维感知劣化趋势；
	// 1 分钟周期会让评分任务在中等规模下持续占用连接池，
	// 导致采集模块写入出现 1-2s 等待尖刺（即运维反馈的"卡一下"）。
	_, err := s.cron.AddFunc("*/5 * * * *", func() {
		s.recomputeAll(context.Background())
	})
	if err != nil {
		log.Printf("【健康度-错误】cron 注册失败: %v", err)
	}
	s.cron.Start()
	log.Println("【健康度-调度】已启动（每 5 分钟全量）")

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
	// 单飞：若上轮评分仍在进行，直接跳过本次触发。
	// robfig/cron 默认允许任务叠加运行（不等待上一次结束），
	// 在 1 分钟周期 + 评分总耗时 > 周期 时会出现 2 轮并发评分，
	// 倍增数据库连接占用，挤兑采集写入。
	s.runningMu.Lock()
	if s.running {
		s.runningMu.Unlock()
		log.Println("【健康度-调度】上轮评分未结束，跳过本次 cron 触发")
		return
	}
	s.running = true
	s.runningMu.Unlock()
	defer func() {
		s.runningMu.Lock()
		s.running = false
		s.runningMu.Unlock()
	}()

	log.Println("【健康度-调度】开始全量评分...")
	start := time.Now()
	sections, err := s.store.GetSections(ctx)
	if err != nil {
		log.Printf("【健康度-错误】获取断面列表失败: %v", err)
		return
	}
	if len(sections) == 0 {
		return
	}

	// 限制并发断面数，避免评分任务集中抢占数据库连接。
	// 每断面 60s 硬超时：单个慢查询不能拖垮整轮。
	sem := make(chan struct{}, RecomputeMaxConcurrency)
	var wg sync.WaitGroup
	for _, sec := range sections {
		sem <- struct{}{}
		wg.Add(1)
		go func(secID int) {
			defer wg.Done()
			defer func() { <-sem }()
			secCtx, cancel := context.WithTimeout(ctx, RecomputePerSectionTimeout)
			defer cancel()
			s.recomputeOne(secCtx, secID, model.HealthTriggerCron)
		}(sec.ID)
	}
	wg.Wait()
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
	// 30d 窗口数据共享：原实现对每个传感器分别拉取 2 次 30d 区间
	// （一次给 30d 速率、一次给 30d 方差），每次约 8000+ 行；
	// 现改为一次拉取、在内存中复用，节省 50% 的 30d 大查询。
	thirtyStart := now.Add(-30 * 24 * time.Hour)

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

		// 30d 速率 + 30d 方差共用同一份原始数据
		var (
			rate30Val float64
			variance  float64
		)
		if data, derr := s.store.GetSensorDataRange(ctx, sensor.ID, thirtyStart, now); derr == nil {
			rate30Val = computeEndpointRateFromData(data)
			variance = computeVarianceFromData(data)
		}

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
	return computeEndpointRateFromData(data)
}

// computeEndpointRateFromData 由原始数据直接计算端点速率。
// 与 computeEndpointRate 解耦：复用于 recomputeOne 内部已缓存的 30d 数据，
// 避免同一窗口重复拉取。
func computeEndpointRateFromData(data []model.SensorData) float64 {
	if len(data) < 2 {
		return 0
	}
	first, last := data[0], data[len(data)-1]
	hours := last.Timestamp.Sub(first.Timestamp).Hours()
	if hours <= 0 {
		return 0
	}
	return (last.Value - first.Value) / hours * 24.0
}

// computeVarianceFromData 由原始数据计算区间标准差。
// 抽离为纯函数，与 scheduler.computeEndpointRate 共享相同的 30d 数据缓存。
func computeVarianceFromData(data []model.SensorData) float64 {
	if len(data) < 2 {
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
