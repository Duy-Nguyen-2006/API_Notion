# Latency Tuning Guide

> Last updated: 2026-06-03

This guide covers how to measure and optimize orchestrator latency to meet the SPEC targets:

| Metric | Target |
|--------|--------|
| TTFT p50 | < 2s |
| Latency p50 | < 3s |
| Latency p99 | < 12s |
| Model round-trips (complex) | ≤ 2 |

---

## 1. Measuring Latency

### 1.1 Agent Trace

Every orchestrator response includes `agent_trace` with per-stage latency:

```json
{
  "agent_trace": {
    "ttft_ms": 1450,
    "model_round_trips": 2,
    "tool_calls": 1,
    "reasoner_calls": 1,
    "stage_latency_ms": {
      "router": 0,
      "tool_exec": 6,
      "context_distill": 3,
      "executor_rtt": 1900,
      "reasoner_rtt": 4200
    }
  }
}
```

Key metrics:
- `ttft_ms`: End-to-end time to first token (includes all stages).
- `stage_latency_ms.router`: Local routing time (should be < 1ms).
- `stage_latency_ms.tool_exec`: Tool execution (typically < 10ms for local tools).
- `stage_latency_ms.reasoner_rtt`: Reasoner API round-trip (dominant cost).
- `stage_latency_ms.executor_rtt`: Executor API round-trip (if used).

### 1.2 Load Testing

Use the load test harness to measure p50/p99 under concurrency:

```bash
# Run with default settings (100 concurrent, 30s)
go test -tags load -run TestOrchestratorLoad -timeout 5m ./tests/load/

# Custom concurrency and duration
LOAD_CONCURRENCY=50 LOAD_DURATION=60s go test -tags load -run TestOrchestratorLoad -timeout 5m ./tests/load/
```

### 1.3 Baseline Script

The existing perf baseline script measures raw API latency:

```bash
N2A_BASE_URL=http://127.0.0.1:8787 N2A_API_KEY=your-key ./scripts/perf/baseline.sh
```

---

## 2. Tuning `hedge_reasoner_ms`

### What It Does

After `hedge_reasoner_ms`, a second parallel request is fired to the reasoner. Whichever responds first wins. This cuts tail latency at the cost of extra API calls.

### How to Tune

1. **Collect reasoner latency distribution** (without hedging):
   ```bash
   # Disable hedging
   # Set hedge_reasoner_ms: 0 in config
   # Run 100+ requests, record reasoner_rtt from agent_trace
   ```

2. **Find the crossover point**: Plot the CDF of reasoner latency. Set `hedge_reasoner_ms` to the **p75** of reasoner latency. This means:
   - 75% of requests complete before the hedge fires (no extra cost).
   - 25% of requests get hedged, cutting their latency significantly.

3. **Default recommendation**: 800ms (based on typical Notion AI latency).

### Tradeoffs

| `hedge_reasoner_ms` | p50 Impact | p99 Impact | Cost Impact |
|---------------------|-----------|-----------|-------------|
| 0 (disabled) | Baseline | High (no hedge) | Baseline |
| 500ms | No change | Lower | +5–10% |
| 800ms (default) | No change | Lower | +10–20% |
| 1200ms | No change | Moderate improvement | +5% |

### Monitoring

Watch `agent_trace.reasoner_calls`:
- `1` = request completed before hedge fired (good).
- `2` = hedge was triggered (expected for slow requests).

---

## 3. Cache Hit Rate Optimization

### 3.1 Tool Cache

- **Key**: `(absolute_path, mtime, size)` — invalidated on `edit_file`.
- **Effectiveness**: High for repeated reads of the same file.
- **Tuning**: No knobs — automatic. Ensure `ToolCacheEnabled: true`.

### 3.2 Reasoner Response Cache

- **Key**: `sha256(query + messages)`.
- **TTL**: 30 minutes (default).
- **Effectiveness**: High for repeated identical queries.
- **Tuning**:
  - Increase TTL for stable workloads.
  - Decrease TTL if stale responses are a problem.

### 3.3 Prefix Cache

- **What**: System prompt + tool schema is kept at the start of every reasoner prompt.
- **Effectiveness**: Enables prefill cache hits on the reasoner side (if supported).
- **Tuning**: Automatic — no configuration needed.

### 3.4 Measuring Cache Hit Rate

From `agent_trace`:
- `reasoner_cache` in `stage_latency_ms` indicates a cache hit (value = 0).
- `prefetch_hit: true` indicates a prefetched tool result was used.

Aggregate across requests:
```
cache_hit_rate = count(reasoner_cache in stage_latency_ms) / total_requests
```

Target: > 50% cache hit rate for representative workloads.

---

## 4. Tool Concurrency Tuning

### 4.1 `MaxConcurrentTools`

Limits parallel tool execution (semaphore). Default: 8.

| Setting | When to Use |
|---------|-------------|
| 4 | Low-resource environments, few concurrent users |
| 8 (default) | Balanced — good for most workloads |
| 16–32 | High-throughput, powerful hardware, many files per request |

### 4.2 `RateLimitPerSecond`

Token-bucket rate limiter for tool execution. Default: 0 (unlimited).

| Setting | When to Use |
|---------|-------------|
| 0 (default) | No rate limiting — tools are local and fast |
| 100–500 | If tools are I/O-bound and disk is saturated |
| 1000+ | If running on slow storage (network mounts) |

### 4.3 Measuring Tool Performance

Tool execution is typically < 10ms. If `stage_latency_ms.tool_exec` is high:
1. Check disk I/O (local SSD vs network mount).
2. Check glob expansion (`read_files("**/*.go")` on large repos).
3. Check search depth (100-match cap helps, but deep trees are slow).

---

## 5. Troubleshooting High Latency

### 5.1 High TTFT (> 2s)

**Symptom**: First token takes > 2s to arrive.

**Causes**:
1. Router classification is slow (unlikely — should be < 1ms).
2. Tool execution blocks the fast path (check if tools are running unnecessarily).
3. Reasoner API is slow (check `reasoner_rtt`).
4. Hedging not configured (check `hedge_reasoner_ms`).

**Fixes**:
- Enable hedging: `hedge_reasoner_ms: 800`.
- Ensure fast-path requests (simple chat) don't trigger tools.
- Check reasoner endpoint health.

### 5.2 High p99 (> 12s)

**Symptom**: 1% of requests take > 12s.

**Causes**:
1. Reasoner tail latency (most common).
2. Executor timeout + fallback.
3. Large file reads.
4. Context distillation on large tool results.

**Fixes**:
- Tune `hedge_reasoner_ms` (see §2).
- Set reasonable timeouts per stage.
- Limit file sizes read by tools.
- Reduce `ContextManager.MaxChars` if distillation is slow.

### 5.3 High Model Round-Trips

**Symptom**: `model_round_trips > 2` for complex requests.

**Causes**:
1. Router not synthesizing direct tool plan (unclear paths).
2. Executor fallback path used unnecessarily.
3. Hedging doubles round-trip count.

**Fixes**:
- Improve router keyword patterns for common requests.
- Provide explicit file paths in prompts (helps router classify).
- Disable hedging if cost is a concern (but p99 will increase).

### 5.4 Low Cache Hit Rate

**Symptom**: Cache hit rate < 30%.

**Causes**:
1. Highly variable queries (each query is unique → no cache hits).
2. Short TTL (expired before reuse).
3. Frequent file edits (invalidates tool cache).

**Fixes**:
- Increase ResponseCache TTL.
- Normalize queries before caching (strip timestamps, session IDs).
- Accept lower tool cache hit rate if files change frequently.

### 5.5 Streaming Not Working

**Symptom**: Client receives full response at once, not streamed.

**Causes**:
1. Reasoner endpoint doesn't support SSE streaming.
2. Proxy buffering between server and client.
3. `stream: false` in request.

**Fixes**:
- Verify reasoner supports SSE (check `Content-Type: text/event-stream`).
- Set `X-Accel-Buffering: no` header if behind nginx.
- Ensure `stream: true` in request.

---

## 6. Quick Reference

| What to Tune | Where | Default | Effect |
|-------------|-------|---------|--------|
| `hedge_reasoner_ms` | `config.json` / request | 800ms | ↓ p99, ↑ cost |
| `MaxConcurrentTools` | `OrchestratorOptions` | 8 | ↑ throughput |
| `RateLimitPerSecond` | `OrchestratorOptions` | 0 | ↓ disk pressure |
| `max_iterations` | `config.json` / request | 1–2 | ↑ flexibility, ↑ latency |
| ResponseCache TTL | `NewResponseCache()` | 30min | ↑ cache hits |
| `MaxChars` (context) | `NewContextManager()` | 24000 | ↓ reasoner tokens |
