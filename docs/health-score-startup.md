# 断面健康度综合评估 - 启动与验收指南

## 1. 启动服务

```bash
# 在项目根目录
docker-compose up -d --build

# 查看日志（重点关注【健康度-调度】与【健康度-评分】前缀）
docker-compose logs -f backend
```

期望日志（每 1 分钟）：

```
【健康度-调度】已启动（每 1 分钟全量）
【健康度-调度】开始全量评分...
【健康度-调度】全量评分完成 断面数=50 耗时=xx
【健康度-评分】断面[1] L3-S001 评分=92.30 等级=excellent 触发=cron scoreID=xx
【健康度-评分】断面[2] L3-S002 评分=91.50 等级=excellent 触发=cron scoreID=xx
...
```

## 2. 验收（5 项必过）

### 2.1 看板展示

```bash
curl -s "http://localhost:8080/api/v1/health-dashboard/rank?line_code=3" | jq '.total, .grade_count'
```

期望：`total >= 50`，`grade_count` 包含 5 个等级键。

### 2.2 评分明细

```bash
curl -s "http://localhost:8080/api/v1/sections/1/health" | jq '.score, (.details | length), (.intermediate | length)'
```

期望：返回 score（总分+等级）、至少 4 条 details、至少 1 条 intermediate。

### 2.3 模拟 3 次告警 → 评分下降

```bash
# 上报异常数据 → 触发分析 → 触发重算（重复 3 次，每轮间隔 > 30s）
# 详见 simulator/cmd/acceptance_health/main.go

# 一键运行
cd simulator
go run ./cmd/acceptance_health
```

期望输出：

```
[3+4] 模拟 3 次告警，验证评分下降及 1 分钟内更新
  ✓ 评分明显下降：基线 90.00(excellent) → 当前 35.00(danger), 降幅=55.00, ...
```

### 2.4 1 分钟内更新

由 [3+4] 同步验证：评分更新耗时 ≤ 60s（cron 间隔 + 事件触发覆盖）。

### 2.5 历史曲线 < 3s

```bash
time curl -s "http://localhost:8080/api/v1/sections/1/health/history?start=$(date -u -d '-30 days' +%FT%TZ)&end=$(date -u +%FT%TZ)&interval=1 day" >/dev/null
```

期望：real 耗时 ≤ 3s。

## 3. 前端验收

1. 浏览器打开 `http://localhost:3000`（前端容器）
2. 首页应显示新增的"断面健康度看板"：
   - 顶部 5 个等级 pill（优良/正常/关注/劣化/危险）
   - 表格列出所有断面（按分值升序，从最差到最好）
   - 列：排名、断面、里程、位置类型、健康分值（带渐变色条）、等级、7d告警数、趋势、计算时间
   - 点击行跳转 `/sections/:id/health`
3. 详情页 `/sections/:id/health` 应展示：
   - 总分卡（环形进度条 + 等级标签）
   - 4 个维度的明细表（告警/趋势/稳定度/完整度）
   - 复核中间数据表（每传感器的多窗口速率、告警数、完整度、方差）
   - 历史健康度曲线 Chart.js 图（avg/min/max 三条线）
4. 任意断面详情页 `/sections/:id` 顶部"查看健康度详情"按钮可跳转

## 4. 关键文件速查

| 文件 | 作用 |
|------|------|
| `backend/internal/healthscore/engine.go` | 纯函数评分引擎 |
| `backend/internal/healthscore/scheduler.go` | cron + 事件触发双通道 |
| `backend/internal/api/health_handler.go` | 4 个 HTTP 端点 |
| `backend/internal/store/store.go` | 健康度相关 SQL（含排名、历史聚合） |
| `backend/internal/model/health.go` | 等级/位置敏感度/权重常量 |
| `docker/init-db.sql` | 3 张健康度表 + 3 年保留策略 |
| `frontend/src/views/HealthDashboard.vue` | 看板子组件 |
| `frontend/src/views/SectionHealthDetail.vue` | 详情页 |
| `frontend/src/stores/health.ts` | Pinia 状态 + 等级显示辅助 |
| `simulator/cmd/acceptance_health/main.go` | 端到端验收脚本 |

## 5. 常见问题

**Q: 看板显示空**
A: 第一次启动后需等 ≤1 分钟 cron 跑一次全量评分；可通过
   `curl -X POST http://localhost:8080/api/v1/sections/1/health/recompute`
   立刻触发 1 号断面的评分。

**Q: 模拟 3 次告警后评分没明显下降**
A: 检查 `data_completeness`：如果完整度太低，所有维度都被压制。完整度 = 7d 内数据点数 / (7*24*12)，需要至少上报 ~2000 条数据才能让完整度 > 1.0（被钳制为 1.0）。也可用如下 SQL 看 7d 内传感器数据点数：
   ```sql
   SELECT sensor_id, COUNT(*) FROM sensor_data WHERE timestamp > NOW() - INTERVAL '7 days' GROUP BY sensor_id;
   ```

**Q: 历史曲线接口报 SQL 错**
A: 确认 TimescaleDB 已启用 hypertable：
   ```sql
   SELECT * FROM timescaledb_information.hypertables WHERE hypertable_name = 'section_health_scores';
   ```
