"""
速率分析算法验证脚本（独立 Python 实现，用于交叉验证 Go 算法逻辑）

使用：
    python verify_rate_algorithm.py

用途：
    1. 不依赖 Go 工具链，可在任何环境快速验证多窗口速率分析逻辑
    2. 作为 store_rate_test.go 的交叉验证（两个实现应当输出一致结果）
    3. 关键告警漏报 bug 的回归用例（先抬升后回落场景）

对应 Go 实现：
    backend/internal/store/store.go::AnalyzeRateFromData

覆盖场景：
    - 阶跃跳变（1h 内 12.3→14.8mm）：rate=60mm/天，必告警
    - 生产场景回归（5min 阶跃 2.5mm）：rate=720mm/天，必告警
    - 缓慢漂移（24h 0.4mm）：rate=0.4mm/天，< 0.5 阈值，不告警
    - 单点阶跃（5min 0.6mm）：rate=172.8mm/天，必告警
"""
import math
from datetime import datetime, timedelta

SLIDING_WINDOW = timedelta(hours=1)
MIN_STEP_INTERVAL = timedelta(minutes=1)


def analyze_rate_from_data(data):
    """Python 版多窗口速率分析，与 Go 实现一一对应"""
    if len(data) < 2:
        return None, "数据点不足"

    first, last = data[0], data[-1]

    # (1) 端点速率
    hours = (last["ts"] - first["ts"]).total_seconds() / 3600.0
    endpoint_rate = (last["v"] - first["v"]) / hours * 24.0 if hours > 0 else 0.0

    # (2) 1h 滑动窗口最大速率
    max_sliding = 0.0
    sliding_meta = {}
    for i in range(len(data)):
        for j in range(i + 1, len(data)):
            span = data[j]["ts"] - data[i]["ts"]
            if span < SLIDING_WINDOW:
                continue
            h = span.total_seconds() / 3600.0
            if h <= 0:
                break
            rate = (data[j]["v"] - data[i]["v"]) / h * 24.0
            if abs(rate) > abs(max_sliding):
                max_sliding = rate
                sliding_meta = {"i": i, "j": j, "span_h": h}
            break

    # (3) 相邻点阶跃最大速率
    max_step = 0.0
    step_meta = {}
    for i in range(1, len(data)):
        span = data[i]["ts"] - data[i - 1]["ts"]
        if span < MIN_STEP_INTERVAL:
            continue
        h = span.total_seconds() / 3600.0
        if h <= 0:
            continue
        rate = (data[i]["v"] - data[i - 1]["v"]) / h * 24.0
        if abs(rate) > abs(max_step):
            max_step = rate
            step_meta = {"i": i - 1, "j": i, "span_h": h}

    # (4) 最严
    rate, source = endpoint_rate, "endpoint"
    if abs(max_sliding) > abs(rate):
        rate, source = max_sliding, "sliding"
    if abs(max_step) > abs(rate):
        rate, source = max_step, "step"

    return {
        "endpoint": endpoint_rate,
        "sliding": max_sliding,
        "step": max_step,
        "rate": rate,
        "source": source,
    }, None


def case_step_then_revert():
    """用例1: 24h 12.3 -> 14.8 (1h阶跃) -> 12.5 (回落)"""
    t0 = datetime(2026, 7, 16, 0, 0, 0)
    data = [
        {"v": 12.3, "ts": t0},
        {"v": 14.8, "ts": t0 + timedelta(hours=1)},
        {"v": 12.5, "ts": t0 + timedelta(hours=24)},
    ]
    return data


def case_regression_bug():
    """用例2: 真实生产场景 - 1h30m 后 5min 阶跃 2.5mm, 22h 缓慢回落"""
    t0 = datetime(2026, 7, 16, 0, 0, 0)
    data = [
        {"v": 12.3, "ts": t0},
        {"v": 12.3, "ts": t0 + timedelta(minutes=90)},
        {"v": 14.8, "ts": t0 + timedelta(minutes=95)},  # 5min 阶跃 2.5mm
        {"v": 12.5, "ts": t0 + timedelta(hours=24)},    # 22h 缓慢回落
    ]
    return data


def case_slow_drift():
    """用例3: 缓慢漂移 12.0 -> 12.4, 24h"""
    t0 = datetime(2026, 7, 16, 0, 0, 0)
    return [
        {"v": 12.0 + 0.05 * i, "ts": t0 + timedelta(hours=3 * i)}
        for i in range(9)
    ]


def case_single_step():
    """用例4: 5min 间隔上 0.6mm 阶跃"""
    t0 = datetime(2026, 7, 16, 0, 0, 0)
    return [
        {"v": 5.0, "ts": t0},
        {"v": 5.6, "ts": t0 + timedelta(minutes=5)},
        {"v": 5.0, "ts": t0 + timedelta(minutes=10)},
    ]


def main():
    cases = [
        ("先抬升后回落 (1h阶跃)", case_step_then_revert(), {"rate_min": 1.0, "source_in": ["sliding", "step"]}),
        ("生产场景回归 (5min阶跃)", case_regression_bug(), {"rate_min": 1.0, "source_in": ["sliding", "step"]}),
        ("缓慢漂移 (无突变)", case_slow_drift(), {"rate_max": 0.5, "source_in": ["endpoint", "sliding", "step"]}),
        ("单点阶跃 (5min,0.6mm)", case_single_step(), {"rate_min": 1.0, "source": "step"}),
    ]
    for name, data, expect in cases:
        result, err = analyze_rate_from_data(data)
        if err:
            print(f"[FAIL] {name}: {err}")
            continue
        ok = True
        if "rate_min" in expect and abs(result["rate"]) < expect["rate_min"]:
            print(f"[FAIL] {name}: rate {result['rate']:.3f} < min {expect['rate_min']}")
            ok = False
        if "rate_max" in expect and abs(result["rate"]) >= expect["rate_max"]:
            print(f"[FAIL] {name}: rate {result['rate']:.3f} >= max {expect['rate_max']}")
            ok = False
        if "source" in expect and result["source"] != expect["source"]:
            print(f"[FAIL] {name}: source {result['source']} != {expect['source']}")
            ok = False
        if "source_in" in expect and result["source"] not in expect["source_in"]:
            print(f"[FAIL] {name}: source {result['source']} not in {expect['source_in']}")
            ok = False
        if ok:
            print(f"[PASS] {name}: rate={result['rate']:+.3f} (endpoint={result['endpoint']:+.3f}, "
                  f"sliding={result['sliding']:+.3f}, step={result['step']:+.3f}, src={result['source']})")


if __name__ == "__main__":
    main()
