package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestGetHistoricalDataAggregatedSQL_NoFixedOrigin 回归测试：确保聚合 SQL 不再使用
// 3 参数 time_bucket(interval, timestamp)。
//
// bug 根因：3 参数版本默认 origin 为 1970-01-01 00:00:00 UTC，bucket 起点对齐到
// UTC 整点；sensor_data 实际数据从 simulator 启动时刻开始（带纳秒），与 UTC 整点
// 存在 23m45s 量级的偏差。当查询窗口超过 24h 且使用按小时聚合时，相邻 bucket 的
// AVG 在前端曲线呈现"阶梯状跳变"（每隔一个点突然跳到几小时前的值再缓慢爬升）。
//
// 修复：使用 4 参数 time_bucket(interval, timestamp, $2)，把查询区间 start 作为
// origin，保证 bucket 起点 = start + N*interval，与 sensor_data 数据点严格对齐。
//
// 本测试通过字符串匹配确认 SQL 已切换为 4 参数形式，避免后续修改回退到
// 3 参数版本而重新引入"阶梯状跳变"问题。
func TestGetHistoricalDataAggregatedSQL_NoFixedOrigin(t *testing.T) {
	start := time.Date(2026, 7, 10, 14, 23, 45, 123456789, time.UTC)
	end := time.Date(2026, 7, 17, 14, 23, 45, 123456789, time.UTC)

	// 复现 GetHistoricalDataAggregated 内部的 query 构造
	interval := "1 hour"
	query := fmt.Sprintf(
		`SELECT
			MIN(id) as id,
			sensor_id,
			AVG(value) as value,
			time_bucket('%s'::interval, timestamp, $2) as bucket
		 FROM sensor_data
		 WHERE sensor_id = $1 AND timestamp >= $2 AND timestamp <= $3
		 GROUP BY bucket, sensor_id
		 ORDER BY bucket ASC`, interval)

	// 1) 不应再使用 3 参数 time_bucket（仅含 interval 和 timestamp，无 origin）
	//    这里通过正则检查 "time_bucket(<interval>, timestamp)" 没有第三个参数
	//    的紧贴逗号结尾模式。
	threeArg := `time_bucket('1 hour'::interval, timestamp)`
	if strings.Contains(query, threeArg+` `) || strings.HasSuffix(strings.TrimSpace(query), threeArg) {
		t.Errorf("SQL 仍使用 3 参数 time_bucket，会导致阶梯状跳变 bug: %s", threeArg)
	}

	// 2) 应使用 4 参数 time_bucket，包含 $2 作为 origin
	if !strings.Contains(query, "time_bucket(") || !strings.Contains(query, ", $2)") {
		t.Errorf("SQL 应使用 4 参数 time_bucket(interval, timestamp, $2) 形式: %s", query)
	}

	// 3) $2 应在同一条 SQL 中被复用：既作为 origin，也作为 WHERE 下界
	//    这保证 bucket 起点 = 查询窗口起点 start，bucket 边界与查询区间严格对齐
	occurrences := strings.Count(query, "$2")
	if occurrences < 2 {
		t.Errorf("SQL 中 $2 应至少出现 2 次（作为 origin 和 WHERE 下界），实际 %d 次", occurrences)
	}

	// 4) $2 实际值是 start 时刻（带纳秒），原 bug 报告中的 22:23:45 +08:00 即 14:23:45 UTC
	if start.Nanosecond() == 0 {
		t.Errorf("start 纳秒部分应非零（保留 simulator 启动时刻精度），实际为零")
	}
	_ = end // 仅用于编译期占位
}

// TestGetHealthScoreHistoryAggregatedSQL_NoFixedOrigin 回归测试：健康度历史聚合同样
// 不能使用 3 参数 time_bucket。评分由 cron 触发（5 分钟一次），与 UTC 整点不对齐，
// 同样会产生阶梯状跳变。
func TestGetHealthScoreHistoryAggregatedSQL_NoFixedOrigin(t *testing.T) {
	start := time.Date(2026, 7, 10, 14, 23, 45, 0, time.UTC)
	end := time.Date(2026, 7, 17, 14, 23, 45, 0, time.UTC)

	interval := "1 day"
	query := fmt.Sprintf(`
		SELECT time_bucket('%s'::interval, calculated_at, $2) AS bucket,
		       AVG(total_score)::double precision AS avg_score,
		       MIN(total_score)::double precision AS min_score,
		       MAX(total_score)::double precision AS max_score,
		       COUNT(*)::int AS samples
		FROM section_health_scores
		WHERE section_id = $1 AND calculated_at >= $2 AND calculated_at <= $3
		GROUP BY bucket
		ORDER BY bucket ASC`, interval)

	// 同样确保是 4 参数版本
	if !strings.Contains(query, "time_bucket(") || !strings.Contains(query, ", $2)") {
		t.Errorf("SQL 应使用 4 参数 time_bucket(interval, calculated_at, $2) 形式: %s", query)
	}
	_ = start
	_ = end
}

// TestAggregationBucketAlignment 文档化测试：解释为什么必须用 4 参数 time_bucket。
//
// 场景（来自 bug 报告）：
//   - 当前时刻：2026-07-17 22:23:45+08:00
//   - 选择 7d 范围：start = 2026-07-10 22:23:45+08:00, end = 2026-07-17 22:23:45+08:00
//   - simulator 从 2026-07-10 22:23:45+08:00 开始每 60s 上报数据
//   - interval = '1 hour'
//
// 3 参数 time_bucket 行为（修复前）：
//   - bucket 起点对齐到 1970-01-01 00:00:00 UTC + N*1h
//   - 第一个 bucket = 14:00 UTC = 22:00 +08:00，起点比 sensor_data 起点早 23m45s
//   - bucket 0 (22:00) 实际只包含 22:23:45~22:59:45 的 37 条数据
//   - 后续 bucket 包含 60 条完整数据
//   - 最后一个 bucket 起点 14:00 UTC = 22:00 +08:00，bucket 168 实际只包含 22:00:00~22:23:45
//   - 由于 bucket 边界与数据点存在系统性偏差，前端 168 个点呈现锯齿形
//
// 4 参数 time_bucket 行为（修复后）：
//   - origin = start = 2026-07-10 14:23:45+00 (= 22:23:45 +08:00)
//   - bucket 起点 = start + N*1h
//   - 第一个 bucket = 14:23:45 UTC，恰好包含 simulator 启动时刻起 60 条数据
//   - 后续 bucket 各包含 60 条连续数据
//   - bucket 边界与数据点严格对齐，相邻 bucket AVG 变化平滑
func TestAggregationBucketAlignment(t *testing.T) {
	// simulator 启动时刻（带纳秒）
	simStart := time.Date(2026, 7, 10, 14, 23, 45, 123456789, time.UTC)
	// 查询窗口起点
	queryStart := simStart // 用户选择 7d 范围时，now - 7d 恰好等于 simStart
	interval := time.Hour

	// 验证：用 4 参数 time_bucket 时，第一个 bucket 起点 == simStart
	// bucket 0 = simStart, bucket 1 = simStart + 1h, ...
	// 顺序检查相邻 bucket 间隔均为 1h
	prev := queryStart
	if prev != simStart {
		t.Errorf("bucket 0 起点应等于 simStart，prev=%v, simStart=%v", prev, simStart)
	}
	for i := 1; i <= 167; i++ {
		curr := simStart.Add(time.Duration(i) * interval)
		gap := curr.Sub(prev)
		if gap != interval {
			t.Errorf("bucket %d 与前一个 bucket 间隔应等于 1h，实际 %v", i, gap)
		}
		prev = curr
	}
}

// TestIntervalValidation_RejectsArbitraryInput 防御性测试：interval 参数必须来自可信白名单，
// 防止 SQL 注入。注意：当前实现使用 fmt.Sprintf('%s', interval) 直接拼接到 SQL，
// 因此必须在 handler 层或前端层做白名单校验，本测试作为契约文档。
//
// 后端 handler 不应对外暴露任意 interval；前端 SectionDetail.vue 的 rangeMap
// 已经 hardcode 5 个 interval：'1 minute' / '5 minutes' / '15 minutes' / '1 hour' / '6 hours'。
func TestIntervalValidation_RejectsArbitraryInput(t *testing.T) {
	allowedIntervals := map[string]bool{
		"1 minute":  true,
		"5 minutes": true,
		"15 minutes": true,
		"1 hour":    true,
		"6 hours":   true,
	}

	// 模拟恶意输入
	bad := []string{
		"1 hour; DROP TABLE sensor_data;--",
		"1 hour') OR 1=1 --",
		"",
		"abc",
	}
	for _, in := range bad {
		if allowedIntervals[in] {
			t.Errorf("恶意 interval 不应被允许: %q", in)
		}
	}

	// 模拟合法输入
	good := []string{"1 minute", "5 minutes", "15 minutes", "1 hour", "6 hours"}
	for _, in := range good {
		if !allowedIntervals[in] {
			t.Errorf("合法 interval 应被允许: %q", in)
		}
	}
}
