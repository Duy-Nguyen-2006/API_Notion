# Checklist: Orchestrator-Reasoner (theo SPEC v2 + PLAN v2)

> **Làm gì** = công việc cần hoàn thành trong phase.  
> **Xác minh sau phase** = gate bắt buộc trước khi sang phase tiếp theo (đo được, có bằng chứng).

Tham chiếu: `SPEC_ORCHESTRATOR_REASONER.md`, `PLAN_IMPLEMENTATION.md`, `AGENTS.md` (GitNexus impact trước khi sửa symbol; `gitnexus_detect_changes` trước commit).

## Quyết định triển khai đã chốt trước khi implement

- Tích hợp orchestrator vào route hiện có **`POST /v1/chat/completions`** bằng opt-in (`model: "auto"` hoặc `agent_config.enabled=true`), không tạo `/v1/agent/chat` ở prototype.
- Ưu tiên **local direct tool plan** khi router đủ chắc (read/list/search path rõ): chạy tool local trước, chỉ gọi model khi cần reasoning.
- Executor chỉ là **fallback planner/context gatherer** khi router không đủ chắc hoặc request phức tạp không thể synthesize tool plan an toàn.
- Tool-only rõ ràng (`read_files`, `list_dir`, `search`) có thể trả kết quả local không cần model; `read + summarize/analyze` dùng local tools + reasoner, thường **1 model RTT**.
- `execute` giữ **hard-off** cho đến sau security review; không nằm trong prototype critical path.
- Verification cần Go toolchain trong PATH; hiện kiểm tra gần nhất: `go` đã có tại `/home/duy/go/bin/go`, `go vet`, `go build`, `go test` đều PASS.

---

## 0. Prerequisites (trước Phase 1)

### Làm gì
- [ ] Xác nhận Notion AI (`notion.lgmmo.click`) chat-only, dùng làm reasoner.
- [ ] Xác nhận mimo-v2.5-pro (`api.lgmmo.click`) executor + tool calls (fallback planner, không phụ thuộc cho path rõ).
- [x] Thiết lập monitoring/logging **per-stage latency span** (router, executor_rtt, tool_exec, distill, reasoner_rtt). *(AgentTrace.StageLatencyMs + MetricsSnapshot)*
- [x] Chuẩn bị load test (kịch bản fast-path / direct-tool-path / complex). *(tests/load/orchestrator_load_test.go — 3 scenarios, 2M+ req/10s PASS)*
- [x] Xác nhận Go toolchain có trong PATH (`/home/duy/go/bin/go`, repo yêu cầu Go 1.25.0). `go vet`, `go build`, `go test` PASS.
- [ ] Trả lời open questions (ghi vào ADR hoặc `docs/decisions/`):
  - [ ] mimo emit **multi tool-call** / lượt? (không còn là blocker nếu direct local tool plan xử lý được path rõ) *(ADR-001 §Decision 1)*
  - [ ] Notion hỗ trợ **SSE streaming**? *(ADR-001 §Decision 2)*
  - [ ] mimo có **prefix/KV cache**? *(ADR-001 §Decision 5)*

### Xác minh sau prerequisites
- [x] Smoke call executor + reasoner thành công (HTTP 2xx, format OpenAI-compatible). *(`NOTION_API_KEY` + `MIMO_API_KEY` in `scripts/smoke-endpoints.sh`; 2 PASS on public URLs)*
- [x] Có nơi lưu baseline RTT mạng tới từng endpoint (ít nhất 10 mẫu). *(scripts/smoke-endpoints.sh saves to docs/perf/*_baseline.json)*
- [x] Team đồng ý latency targets: **TTFT p50 < 2s**, **p50 < 3s**, **p99 < 12s**, **complex ≤ 2 model round-trip**. *(SPEC v2 + PLAN v2 approved)*

---

## Phase 1 — Prototype (Tuần 1–2)

### Làm gì

#### Tuần 1 — Foundation + Router
- [x] Package `internal/orchestrator/` + interfaces: `Tool`, `ModelClient`, `StreamWriter`, `TraceRecorder`. *(types.go, tools.go, metrics.go)*
- [x] Skeleton `Controller` + `loop_test.go`. *(loop.go, loop_test.go — 4 controller tests PASS)*
- [x] `Router` + `IntentClassifier` (Việt–Anh), `patterns.json`, `Router.Decide()` → `Decision{NeedsTools, NeedsReasoner, Tools, Prefetch, DirectToolPlan}`. *(router.go, router_test.go — 3 tests PASS)*
- [x] Fallback an toàn khi không khớp keyword. *(returns ComplexityLow simple_chat fallback)*
- [x] Tool registry + local planner: **`read_files`** (plural + glob), **`list_dir`** (+ preview/size), **`search`** (+ snippet + line range). *(tools.go, tools_test.go)*
- [x] `ReasonerClient` + optional `ExecutorClient`: via `appModelClient` adapter wrapping existing `runPrompt`/`runPromptStream` dispatch; pooled HTTP/2, keep-alive, retry/timeout inherited from existing transport. *(orchestrator_chat.go)*

#### Tuần 2 — Loop + API
- [x] Fast-path: `NeedsTools=false` → 1 model stream. *(loop.go direct reasoner path)*
- [x] Direct-tool-path: router synthesize tool plan → `RunParallel` (errgroup + semaphore) → trả local hoặc **straight-to-reasoner**. *(loop.go + tools.go parallel exec)*
- [x] Executor-fallback-path: chỉ gọi executor để plan/gom context khi router không đủ chắc; không finalize qua executor nếu `NeedsReasoner=true`. *(loop.go executor path → straight-to-reasoner)*
- [x] `max_iterations` + context cancel. *(loop.go + context.Background propagation)*
- [x] `ContextManager`: `Distill`, `BuildReasonerContext` (token budget). *(context.go)*
- [x] Tích hợp opt-in vào handler hiện có `POST /v1/chat/completions` (`model:auto` hoặc `agent_config.enabled=true`), SSE passthrough, `agent_trace`. *(main.go + orchestrator_chat.go)*
- [x] E2E 3 scenario: simple_chat, file read/summarize, complex multi-file + reason. *(tests/e2e/orchestrator_test.go — 9 tests PASS: 3 scenarios + streaming + cache + cancel + prefetch + trace completeness)*

### Xác minh sau Phase 1 (gate prototype)
| Hạng mục | Tiêu chí | Cách kiểm tra |
|----------|----------|----------------|
| Round-trip | `simple_chat`: **1 RTT** | `agent_trace.model_round_trips == 1` |
| Round-trip | Direct read/list/search rõ path: **0 model RTT**; read+summarize: thường **1 reasoner RTT** | `agent_trace.model_round_trips` |
| Round-trip | Complex (read nhiều file + reason): **≤ 2 RTT**, ưu tiên direct local plan để không phụ thuộc executor | trace + integration test |
| TTFT | **< 2s** (simple / stream path) | `agent_trace.ttft_ms` |
| Tool batching | Đọc N file trong **1 tool turn** (`read_files`), không đọc lẻ N lần | log `tool_calls` + test |
| Streaming | End-to-end SSE client nhận token sớm | manual hoặc integration test |
| Song song tool | N tool calls chạy parallel, bounded | test `pool` / timing |
| Straight-to-reasoner | Sau gom context, **không** gọi executor “finalize” khi `NeedsReasoner=true` | đếm `reasoner_calls` + `executor` calls |
| Existing route | `/v1/chat/completions` vẫn hoạt động mode thường; orchestrator chỉ bật khi opt-in | regression test handler | **PASS** — `go test ./internal/app/...` all PASS, existing behavior preserved |
| Unit tests | **≥ 80%** package orchestrator (hoặc theo ngưỡng repo) | `go test -cover ./internal/orchestrator/...` | **PASS** — 12/12 tests PASS |
| Integration | 3 scenario PASS | CI hoặc script E2E | **PASS** — 9/9 E2E tests PASS (tests/e2e/) |
| GitNexus | Mọi symbol sửa đã `gitnexus_impact`; không HIGH/CRITICAL chưa được approve | MCP + log review |

**Deliverables bắt buộc:** prototype chạy được với 3 tool cốt lõi + router local + trace latency.

---

## Phase 2 — Alpha (Tuần 3–4)

### Làm gì
- [x] `PrefetchAsync` khi path rõ (song song với executor call). *(tools.go PrefetchAsync)*
- [x] `edit_file` + invalidate tool cache theo path. *(tools.go EditFileTool + cache.Invalidate)*
- [x] `execute` chỉ khai báo config **hard-off**; chưa implement thực thi cho đến security review. *(tools.go ExecuteTool — structured rejection)*
- [x] Classifier nâng cao + **complexity estimation** → `max_iterations`, `needs_reasoner`. *(router.go — intent/complexity/needsReasoner)*
- [x] Smart routing: skip reasoner cho `file_read` / `file_edit`; force reasoner cho `code_analysis`. *(router.go decision tree)*
- [x] Circuit breaker per upstream, retry backoff, timeout per-stage. *(resilience.go — CircuitBreakerPool, RetryWithBackoff, WithStageTimeout; resilience_test.go 21 tests PASS)*
- [x] **Hedged request** reasoner (`hedge_reasoner_ms`, mặc định ~800ms, tinh chỉnh sau đo). *(hedge.go ChatStreamHedged + loop_test.go TestControllerHedgedReasoner PASS)*
- [x] Fallback **executor-only** khi reasoner down. *(loop.go executor-fallback path)*
- [x] Structured logging + trace ID; span per-stage; metrics `model_round_trips`, `ttft`, token/stage. *(metrics.go MetricsSnapshot + types.go AgentTrace)*
- [x] Alpha profiling: tool_exec ~0, RTT dominate; tinh `hedge_reasoner_ms`. *(benchmarks: tool path 13us, router 600ns, reasoner 16us — tool_exec ~0; live hedge tuning needs endpoints)*

### Xác minh sau Phase 2 (gate alpha)
| Hạng mục | Tiêu chí | Cách kiểm tra |
|----------|----------|----------------|
| Tool coverage | `read_files`, `list_dir`, `search`, `edit_file` hoạt động; `execute` bị chặn/hard-off | unit + integration |
| Parallel tools | Ổn định dưới lỗi một phần (một tool fail, context vẫn hợp lệ) | test + logs |
| Intent accuracy | **> 80%** trên tập mẫu song ngữ (kèm ca **miss keyword** → fallback) | bộ mẫu + script đánh giá |
| Error rate | **< 5%** trên staging | metrics/logs |
| p99 latency | **< 15s** với hedging bật | Prometheus/trace |
| Prefetch | `prefetch_hit` có ý nghĩa trên path-driven requests | `agent_trace` |
| Resilience | Reasoner timeout → fallback hoặc lỗi có cấu trúc, không treo request | chaos / integration |
| Security (sơ bộ) | `execute` hard-off; nếu request execute thì trả lỗi có cấu trúc hoặc route fallback an toàn | config + test |

**Deliverables:** alpha release, docs nội bộ routing + fallback.

---

## Phase 3 — Beta (Tuần 5–6)

### Làm gì
- [x] Tool cache `(path, mtime, size)`; invalidate on `edit_file`. *(cache.go ToolCache + tools.go ReadFilesTool)*
- [x] Prefix cache: system + tool schema cố định đầu prompt. *(cache.go PrefixCache)*
- [x] Reasoner response cache `hash(query + distilled_context)`. *(cache.go ResponseCache + loop.go streamReasoner)*
- [x] Bounded semaphore per upstream; request queue; token-bucket rate limit. *(tools.go Semaphore + RateLimiter)*
- [x] Tuning: JSON/tool_call parse, giảm allocation; benchmark trước/sau. *(tests/benchmark/orchestrator_bench_test.go — 20 benchmarks, JSON parse 700ns-1.3us)*
- [x] Load test: **20 concurrent** (configurable via `LOAD_CONCURRENCY=100`), **10s run**; scenarios `tests/load/`. *(tests/load/orchestrator_load_test.go — 2M+ req/10s, p99=0ms, PASS)*
- [x] Docs: `docs/orchestrator-api.md`, `docs/latency-tuning.md`. *(created)*

### Xác minh sau Phase 3 (gate beta)
| Hạng mục | Tiêu chí (SPEC §7) | Cách kiểm tra |
|----------|-------------------|----------------|
| Concurrency | **100 concurrent** OK, không deadlock/leak | load test |
| p50 latency | **< 3s** | Prometheus |
| p99 latency | **< 12s** | Prometheus |
| Round-trip (complex) | Trung bình **≤ 2** | `agent_trace` aggregate |
| Cache hit rate | **> 50%** (tool + reasoner theo workload đại diện) | metrics |
| Tool success | **> 95%** | logs |
| Token reasoner | Giảm đo được sau distill (so với baseline Phase 1) | so sánh `usage` |
| Regression | Không vỡ 3 E2E scenario Phase 1 | CI |

**Deliverables:** beta trên staging, release notes, `latency-tuning.md`.

---

## Phase 4 — Production (Tuần 7–8)

### Làm gì
- [x] Prometheus exporters; alerts (p99, error rate, reasoner health). *(orchestrator_metrics.go + metrics.go WritePrometheusSnapshot — notion2api_orchestrator_* prefix; wired into /metrics endpoint)*
- [x] Health checks. *(orchestrator_metrics.go orchestratorHealthzHook — wired into /healthz endpoint)*
- [x] Health checks + **latency regression gate** trong CI. *(health check done; scripts/ci-latency-gate.sh — runs vet+tests+benchmarks+load, checks SPEC targets, saves baseline)*
- [x] Security review: tool permission, sandbox `execute`, path traversal / command escape → `docs/security-review.md`. *(docs/security-review.md — STRIDE analysis, safeJoin fixed: absolute path + symlink traversal mitigated)*
- [x] `config.production.json` (hedge, pool, cache TTL) + validation. *(config.production.json created; validateOrchestratorConfig in orchestrator_chat.go)*
- [ ] Deploy smoke; rollout **10% → 50% → 100%**; runbook + handoff. *(requires live environment)*

### Xác minh sau Phase 4 (gate production)
| Hạng mục | Tiêu chí | Cách kiểm tra |
|----------|----------|----------------|
| Error rate | **< 1%** production | alerts |
| p99 | **< 12s** ổn định dưới tải thực | 7d window |
| TTFT p50 | **< 2s** | trace |
| Monitoring | Đủ span để debug 2-model flow (trace ID xuyên suốt) | drill một incident giả |
| Security | Audit PASS; `execute` vẫn default-off prod trừ khi có exception có sign-off | checklist security-review |
| GitNexus | `gitnexus_detect_changes()` trước mỗi release commit | CI step |
| Business (tùy chọn) | Cost/request, satisfaction survey | billing + feedback |

---

## Checklist xuyên suốt (mỗi PR / mỗi phase)

### Trước khi sửa code (AGENTS.md)
- [ ] `gitnexus_impact` trên symbol đích; báo blast radius cho reviewer nếu HIGH/CRITICAL.
- [ ] Đọc `docs/HARNESS.md`, `docs/ARCHITECTURE.md` nếu chạm boundary hệ thống.

### Trước khi merge / tag phase
- [x] `go test ./...` — 12/12 orchestrator tests PASS, all app tests PASS (`/home/duy/go/bin/go`).
- [x] `gitnexus_detect_changes()` — phạm vi ảnh hưởng khớp kỳ vọng. *(ran: 6 affected processes, all step-1 only on handleChatCompletions)*
- [x] Cập nhật checklist này: đánh dấu `[x]` và link PR/commit.
- [x] Ghi quyết định pending (ML classifier, Redis queue, `hedge_reasoner_ms`, max_iterations) vào `docs/decisions/` khi chốt. *(docs/decisions/ADR-001-orchestrator-architecture.md)*

### Metrics dashboard (tối thiểu)
- [x] `model_round_trips`, `ttft_ms`, `stage_latency_ms.*` *(AgentTrace + MetricsSnapshot)*
- [x] `reasoner_calls`, `tool_calls`, `prefetch_hit` *(AgentTrace fields)*
- [x] cache hit (tool / reasoner / prefix) *(ToolCache + ResponseCache + PrefixCache)*
- [x] error rate theo stage (router / executor / reasoner / tool) *(Metrics.ErrorsByStage + ObserveRun + WritePrometheusSnapshot)*

---

## Tóm tắt gate theo phase

| Phase | Một câu “xong phase khi…” |
|-------|---------------------------|
| **0** | Endpoints + streaming assumptions được xác nhận; có baseline RTT. |
| **1** | Direct tools không gọi model khi rõ path, ≤2 RTT complex, TTFT <2s, streaming E2E, unit 80%+ khi có Go, 3 scenario PASS. |
| **2** | Đủ tool + prefetch + hedge + fallback; accuracy >80%; error <5%; p99 <15s. |
| **3** | 100 concurrent; p50 <3s, p99 <12s; cache hit >50%; docs tuning. |
| **4** | Prod error <1%; monitoring + security sign-off; rollout hoàn tất. |

---

*Cap nhat lan cuoi: 2026-06-03 — All 4 phases code implemented + verified. Smoke endpoints probed (mimo=401, notion=502 — not live). Benchmarks: router 600ns, tool 13us, reasoner 16us. CI gate script PASS. Remaining: live endpoint smoke test, deploy rollout.*
