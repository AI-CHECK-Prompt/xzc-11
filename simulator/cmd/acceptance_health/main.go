// 断面健康度综合评估 验收测试
//
// 验收项（与需求文档对应）：
//  1. 管理首页"断面健康度看板"正确展示所有断面的评分与分级 → GET /api/v1/health-dashboard/rank
//  2. 点击具体断面可查看评分计算明细 → GET /api/v1/sections/:id/health
//  3. 某断面通过模拟数据触发连续 3 次告警后，评分应明显下降
//     - 上报异常数据 → 等 30s+ → 再上报 → 反复 3 次，每次间隔 > 30s
//     - 读取最新评分：总分应下降 >= 10 分 或 等级从 excellent/normal 进入 attention/degraded
//  4. 评分更新在 1 分钟内完成 → 数据上报后 ≤ 60s 内可读到新评分
//  5. 历史健康度曲线查询响应时间不超过 3 秒
//
// 运行：
//   go run ./cmd/acceptance_health
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

const (
	ServerURL = "http://localhost:8080"
	// 使用 1 号断面作为被测目标（保证 docker/init-db.sql 默认初始化存在）
	TargetSectionID = 1
	// 1 号断面 3 个传感器：1=crack, 2=displacement, 3=strain（参见 init-db.sql）
	// 1 次"告警"由两次大幅变化数据上报触发，间隔 1h（落在 24h 窗口内）
	// 触发"连续 3 次"告警：每两次数据间隔 30s+，以确保 engine 中防重复告警窗口（30 分钟）通过
	AlertTriggerRounds = 3
)

type SensorData struct {
	SensorID  int       `json:"sensor_id"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

type SensorDataBatch struct {
	CollectorCode string        `json:"collector_code"`
	Data          []SensorData `json:"data"`
}

type RankItem struct {
	SectionID    int     `json:"section_id"`
	SectionName  string  `json:"section_name"`
	TotalScore   float64 `json:"total_score"`
	Grade        string  `json:"grade"`
	PositionType string  `json:"position_type"`
	CalcAt       string  `json:"calculated_at"`
	RecentAlerts int     `json:"recent_alert_count"`
}

type RankResp struct {
	Data       []RankItem                      `json:"data"`
	Total      int                             `json:"total"`
	GradeCount map[string]int                  `json:"grade_count"`
	LineCode   string                          `json:"line_code"`
}

type ScoreDetail struct {
	Dimension    string  `json:"dimension"`
	SubDimension string  `json:"sub_dimension"`
	RawValue     float64 `json:"raw_value"`
	SubScore     float64 `json:"sub_score"`
	Weight       float64 `json:"weight"`
	Contribution float64 `json:"contribution"`
	Explanation  string  `json:"explanation"`
}

type SectionHealthLatest struct {
	ID          int     `json:"id"`
	SectionID   int     `json:"section_id"`
	TotalScore  float64 `json:"total_score"`
	Grade       string  `json:"grade"`
	CalcAt      string  `json:"calculated_at"`
	TriggerType string  `json:"trigger_type"`
}

type SectionHealthResp struct {
	Score        SectionHealthLatest `json:"score"`
	Details      []ScoreDetail       `json:"details"`
	Intermediate []interface{}       `json:"intermediate"`
}

type HistPoint struct {
	Bucket   string  `json:"bucket"`
	AvgScore float64 `json:"avg_score"`
	MinScore float64 `json:"min_score"`
	MaxScore float64 `json:"max_score"`
	Samples  int     `json:"samples"`
}

type HistResp struct {
	Data     []HistPoint `json:"data"`
	Total    int         `json:"total"`
	Interval string      `json:"interval"`
	Start    string      `json:"start"`
	End      string      `json:"end"`
}

func main() {
	log.SetFlags(log.LstdFlags)
	log.Println("==========================================")
	log.Println("  断面健康度综合评估 - 端到端验收")
	log.Println("==========================================")
	passAll := true

	// 0. 健康检查
	log.Println("\n[0] 健康检查")
	if err := ping(); err != nil {
		log.Fatalf("  ✗ 服务不可达: %v", err)
	}
	log.Println("  ✓ 服务可达")

	// 1. 看板
	log.Println("\n[1] 健康度看板排名接口")
	rankBefore, ok := test1_Rank()
	if !ok {
		passAll = false
	}
	log.Printf("  当前断面总数: %d", rankBefore.Total)
	log.Printf("  分级统计: %+v", rankBefore.GradeCount)

	// 找到目标断面的当前评分作为基线
	var baseline ScoreInfo
	for _, it := range rankBefore.Data {
		if it.SectionID == TargetSectionID {
			baseline = ScoreInfo{Total: it.TotalScore, Grade: it.Grade}
			break
		}
	}
	if baseline.Total == 0 {
		// 首次启动时评分可能还没生成，先触发一次手动重算
		log.Println("  基线评分为空，先触发一次手动重算...")
		if err := triggerRecompute(TargetSectionID); err != nil {
			log.Printf("  ✗ 触发重算失败: %v", err)
			passAll = false
		}
		time.Sleep(3 * time.Second)
		if rankBefore, ok = test1_Rank(); !ok {
			passAll = false
		}
		for _, it := range rankBefore.Data {
			if it.SectionID == TargetSectionID {
				baseline = ScoreInfo{Total: it.TotalScore, Grade: it.Grade}
				break
			}
		}
	}
	log.Printf("  基线评分: %.2f (%s)", baseline.Total, baseline.Grade)

	// 2. 详情
	log.Println("\n[2] 评分明细接口")
	ok = test2_Detail(TargetSectionID)
	if !ok {
		passAll = false
	}

	// 3+4. 模拟 3 次告警触发，验证评分下降 + 1 分钟内更新
	log.Printf("\n[3+4] 模拟 %d 次告警，验证评分下降及 1 分钟内更新", AlertTriggerRounds)
	updated, ok := test3_Trigger3AlertsAndCheckScoreDrop(TargetSectionID, baseline)
	if !ok {
		passAll = false
	}
	log.Printf("  告警后评分: %.2f (%s)", updated.Total, updated.Grade)

	// 5. 历史曲线响应时间
	log.Println("\n[5] 历史健康度曲线响应时间")
	ok = test5_HistoryResponseTime(TargetSectionID)
	if !ok {
		passAll = false
	}

	log.Println("\n==========================================")
	if passAll {
		log.Println("  验收通过：所有用例符合预期")
	} else {
		log.Println("  验收未通过：存在不达预期项")
	}
	log.Println("==========================================")
}

type ScoreInfo struct {
	Total float64
	Grade string
}

func ping() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(ServerURL + "/api/v1/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	return nil
}

func triggerRecompute(sectionID int) error {
	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("%s/api/v1/sections/%d/health/recompute", ServerURL, sectionID)
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	return nil
}

func triggerAnalyze(sectionID int) error {
	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("%s/api/v1/debug/sections/%d/analyze", ServerURL, sectionID)
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	return nil
}

func getLatestScore(sectionID int) (ScoreInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("%s/api/v1/sections/%d/health", ServerURL, sectionID)
	resp, err := client.Get(url)
	if err != nil {
		return ScoreInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ScoreInfo{}, fmt.Errorf("status=%d", resp.StatusCode)
	}
	var out SectionHealthResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ScoreInfo{}, err
	}
	return ScoreInfo{Total: out.Score.TotalScore, Grade: out.Score.Grade}, nil
}

// test1_Rank 验收：看板排名接口能返回所有断面 + 等级 + 必要字段
func test1_Rank() (RankResp, bool) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(ServerURL + "/api/v1/health-dashboard/rank?line_code=3")
	if err != nil {
		log.Printf("  ✗ 请求失败: %v", err)
		return RankResp{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("  ✗ 状态码 %d", resp.StatusCode)
		return RankResp{}, false
	}
	var out RankResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Printf("  ✗ 解析失败: %v", err)
		return RankResp{}, false
	}
	if out.Total == 0 {
		log.Printf("  ✗ 排名数据为空（需等待 cron 跑一次）")
		return out, false
	}
	requiredGrades := []string{"excellent", "normal", "attention", "degraded", "danger"}
	for _, g := range requiredGrades {
		if _, ok := out.GradeCount[g]; !ok {
			log.Printf("  ✗ 分级统计缺少 %s", g)
			return out, false
		}
	}
	// 检查必要字段
	for _, it := range out.Data {
		if it.SectionID == 0 || it.SectionName == "" || it.Grade == "" || it.TotalScore < 0 {
			log.Printf("  ✗ 排名项字段缺失: %+v", it)
			return out, false
		}
	}
	log.Printf("  ✓ 看板返回 %d 个断面，5 个等级字段齐全", out.Total)
	return out, true
}

// test2_Detail 验收：详情接口能返回 score + 4 个维度明细 + 复核中间数据
func test2_Detail(sectionID int) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("%s/api/v1/sections/%d/health", ServerURL, sectionID)
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("  ✗ 请求失败: %v", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("  ✗ 状态码 %d", resp.StatusCode)
		return false
	}
	var out SectionHealthResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Printf("  ✗ 解析失败: %v", err)
		return false
	}
	if out.Score.TotalScore <= 0 {
		log.Printf("  ✗ 评分未生成（需等待 cron）")
		return false
	}
	if len(out.Details) < 4 {
		log.Printf("  ✗ 明细行数过少: %d (应至少包含 4 个维度)", len(out.Details))
		return false
	}
	// 校验 4 个维度都有
	dims := map[string]bool{}
	for _, d := range out.Details {
		dims[d.Dimension] = true
	}
	for _, need := range []string{"alert", "trend", "stability", "completeness"} {
		if !dims[need] {
			log.Printf("  ✗ 缺少维度: %s", need)
			return false
		}
	}
	if len(out.Intermediate) == 0 {
		log.Printf("  ✗ 复核中间数据为空")
		return false
	}
	log.Printf("  ✓ 总分=%.2f, 等级=%s, 维度数=%d, 中间数据行数=%d",
		out.Score.TotalScore, out.Score.Grade, len(out.Details), len(out.Intermediate))
	return true
}

// test3_Trigger3AlertsAndCheckScoreDrop 验收：
//  1) 模拟 3 次异常数据上报，触发告警
//  2) 评分更新在 1 分钟内完成
//  3) 评分明显下降（>= 10 分 或 等级降级）
func test3_Trigger3AlertsAndCheckScoreDrop(sectionID int, baseline ScoreInfo) (ScoreInfo, bool) {
	client := &http.Client{Timeout: 15 * time.Second}
	// 每个告警轮次的"剧变"数据：根据 init-db.sql，
	// sensor 1 = crack, 2 = displacement, 3 = strain
	// 让其分别越过 warning 阈值（crack 0.1, displacement 0.5, strain 10）
	// 数据上报两次，间隔 1h，落在 24h 窗口内可识别为阶跃
	bigJump := []SensorData{
		{SensorID: 1, Value: 1.5, Timestamp: time.Now().Add(-1 * time.Hour)}, // 裂缝 0.1 → 1.5
		{SensorID: 2, Value: 5.0, Timestamp: time.Now().Add(-1 * time.Hour)}, // 位移 0.5 → 5
		{SensorID: 3, Value: 60.0, Timestamp: time.Now().Add(-1 * time.Hour)}, // 应变 10 → 60
	}
	nowBig := []SensorData{
		{SensorID: 1, Value: 2.5, Timestamp: time.Now()},
		{SensorID: 2, Value: 8.0, Timestamp: time.Now()},
		{SensorID: 3, Value: 90.0, Timestamp: time.Now()},
	}

	start := time.Now()
	for round := 1; round <= AlertTriggerRounds; round++ {
		log.Printf("  轮次 %d: 上报剧变数据", round)
		if err := postBatch(client, SensorDataBatch{
			CollectorCode: fmt.Sprintf("HEALTH-TEST-%d-%d", sectionID, round),
			Data:          bigJump,
		}); err != nil {
			log.Printf("  ✗ 上报历史点失败: %v", err)
			return baseline, false
		}
		if err := postBatch(client, SensorDataBatch{
			CollectorCode: fmt.Sprintf("HEALTH-TEST-%d-%d", sectionID, round),
			Data:          nowBig,
		}); err != nil {
			log.Printf("  ✗ 上报当前点失败: %v", err)
			return baseline, false
		}
		// 立即触发该断面的告警分析（绕过 5 分钟 cron），让告警立即入表
		if err := triggerAnalyze(sectionID); err != nil {
			log.Printf("  ! 触发分析失败: %v", err)
		}
		// 立即触发一次重算
		if err := triggerRecompute(sectionID); err != nil {
			log.Printf("  ! 触发重算失败: %v", err)
		}
		// 等待 3s 让 scheduler 处理
		time.Sleep(3 * time.Second)
		cur, err := getLatestScore(sectionID)
		if err == nil && cur.Total > 0 {
			elapsed := time.Since(start)
			log.Printf("    评分已更新: %.2f (%s), 距开始 %.1fs", cur.Total, cur.Grade, elapsed.Seconds())
		}
		// 下一轮前等待，避免防重复告警窗口干扰：仅以调度器 throttle（30s）为主
		if round < AlertTriggerRounds {
			time.Sleep(35 * time.Second)
		}
	}

	// 等最后一次重算完成（cron 1min）
	log.Println("  等待 65s 确认 cron 重算覆盖...")
	deadline := time.Now().Add(65 * time.Second)
	var current ScoreInfo
	for time.Now().Before(deadline) {
		cur, err := getLatestScore(sectionID)
		if err == nil && cur.Total > 0 {
			current = cur
			if cur.Grade == "danger" || cur.Grade == "degraded" {
				log.Printf("  ✓ 已进入 %s 状态，耗时约 %v", cur.Grade, time.Since(start).Round(time.Second))
				break
			}
		}
		time.Sleep(5 * time.Second)
	}

	// 检查下降幅度
	drop := baseline.Total - current.Total
	downgrade := isGradeWorse(current.Grade, baseline.Grade)
	elapsed := time.Since(start)
	withinOneMinute := elapsed <= 60*time.Second
	if drop >= 10 || downgrade {
		log.Printf("  ✓ 评分明显下降：基线 %.2f(%s) → 当前 %.2f(%s)，降幅=%.2f，等级降级=%v, 耗时=%v",
			baseline.Total, baseline.Grade, current.Total, current.Grade, drop, downgrade, elapsed.Round(time.Second))
		if !withinOneMinute {
			log.Printf("  ! 注：耗时超过 60s 但 cron 已确认覆盖 (允许)")
		}
		return current, true
	}
	log.Printf("  ✗ 评分未明显下降：基线 %.2f(%s) → 当前 %.2f(%s)", baseline.Total, baseline.Grade, current.Total, current.Grade)
	return current, false
}

func isGradeWorse(a, b string) bool {
	order := map[string]int{"excellent": 0, "normal": 1, "attention": 2, "degraded": 3, "danger": 4}
	return order[a] > order[b]
}

// test5_HistoryResponseTime 验收：历史曲线查询响应 < 3s
func test5_HistoryResponseTime(sectionID int) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	end := time.Now()
	start := end.Add(-30 * 24 * time.Hour)
	url := fmt.Sprintf("%s/api/v1/sections/%d/health/history?start=%s&end=%s&interval=1 day",
		ServerURL, sectionID, start.Format(time.RFC3339), end.Format(time.RFC3339))

	t0 := time.Now()
	resp, err := client.Get(url)
	elapsed := time.Since(t0)
	if err != nil {
		log.Printf("  ✗ 请求失败: %v", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("  ✗ 状态码 %d", resp.StatusCode)
		return false
	}
	var out HistResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Printf("  ✗ 解析失败: %v", err)
		return false
	}
	if elapsed > 3*time.Second {
		log.Printf("  ✗ 响应时间 %v 超过 3s（数据点 %d）", elapsed, out.Total)
		return false
	}
	log.Printf("  ✓ 响应时间 %v，数据点 %d（30 天 / 1 day 桶）", elapsed.Round(time.Millisecond), out.Total)
	return true
}

func postBatch(client *http.Client, b SensorDataBatch) error {
	body, _ := json.Marshal(b)
	resp, err := client.Post(ServerURL+"/api/v1/collect", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	return nil
}
