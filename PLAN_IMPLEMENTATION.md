# PLAN: Implementation Orchestrator-Reasoner (v2 — Performance-First)

> Viết lại từ v1. Thứ tự triển khai được **sắp lại theo ưu tiên hiệu năng**: dựng đường đi latency tối ưu (router upfront → tool song song → straight-to-reasoner → streaming) NGAY từ prototype, thay vì để tối ưu xuống cuối.

## 1. Executive Summary

**Goal**: Hybrid Orchestrator-Reasoner — local router/tool planner (ưu tiên path rõ) + mimo-v2.5-pro (executor fallback, gom context qua tool khi router không đủ chắc) + Notion AI (reasoner, suy luận ≤ 1 lần), điều phối bởi **router local**. Tối ưu **độ trễ thấp nhất** và **hiệu quả cao nhất**.

**Latency targets**: p50 < 3s, p99 < 12s, TTFT < 2s, direct tool-only = 0 model RTT, read+reason = 1 RTT, worst-case complex fallback ≤ 2 model round-trip.

**Timeline**: 8 tuần (2 prototype → 2 alpha → 2 beta → 2 production).

**Effort**: ~200–260h (1 dev chính + 1 senior review).

**Risk**: Medium (kiến trúc rõ, dependency reasoner là rủi ro chính về tail latency).

## 2. Prerequisites

### 2.1 Infrastructure
- [x] Notion AI API (`notion.lgmmo.click`) — hoạt động (chat-only, làm reasoner).
- [x] mimo-v2.5-pro API (`api.lgmmo.click`) — hoạt động (executor, tool calls).
- [ ] Go toolchain available in PATH (`go version`; repo yêu cầu Go 1.25.0). Ghi chú kiểm tra gần nhất: `go` chưa có trong PATH.
- [ ] Monitoring/logging (per-stage latency span).
- [ ] Load testing tools.

### 2.2 Knowledge
- [x] OpenAI API format, Go concurrency.
- [ ] Xác nhận: Notion hỗ trợ SSE streaming? mimo emit multi tool-call/lượt? mimo có prefix cache? (multi tool-call không còn là blocker nhờ direct local planner).

### 2.3 Resources
- 1 Senior Engineer (review kiến trúc + latency gate).
- 1 Developer (implementation).
- Budget API ~$50–100/tháng test (hedging có thể tăng nhẹ chi phí reasoner).

## 3. Detailed Implementation Plan

### Phase 1: Prototype (Week 1-2) — dựng latency-optimal path ngay

#### Week 1: Foundation + Router

**Day 1-2: Architecture + interfaces**
```
Tasks:
- [ ] Package internal/orchestrator/
- [ ] Interfaces: Tool, ModelClient, StreamWriter, TraceRecorder
- [ ] Skeleton Controller + unit test skeleton
Files:
- internal/orchestrator/{agent,tool,model,loop}.go
- internal/orchestrator/loop_test.go
```

**Day 3: Router LOCAL (quyết reasoning upfront)**
```
Tasks:
- [ ] IntentClassifier rule + heuristic (song ngữ Việt-Anh)
- [ ] Router.Decide() trả Decision{NeedsTools, NeedsReasoner, Tools, Prefetch, DirectToolPlan}
- [ ] Direct local tool plan khi path/search/list rõ; fallback an toàn khi không khớp keyword
- [ ] Router tests (đặc biệt ca không có keyword)
Files:
- internal/orchestrator/router.go, classifier.go, patterns.json
```

**Day 4: Tool Registry — thiết kế giảm round-trip**
```
Tasks:
- [ ] Tool interface
- [ ] Local direct tool planner
- [ ] read_files (PLURAL + glob)   <-- ưu tiên
- [ ] list_dir (kèm preview/size)
- [ ] search (trả snippet + line range)
- [ ] Tool tests
Files:
- internal/orchestrator/tools/{registry,read_files,list_dir,search}.go
```

**Day 5: Model Clients + connection pooling**
```
Tasks:
- [ ] ReasonerClient (Notion) — pooled, max_tokens chặt
- [ ] ExecutorClient (mimo) — fallback planner, reuse *http.Client, HTTP/2, keep-alive
- [ ] Streaming support (SSE) cho cả hai
- [ ] Retry/timeout/circuit breaker cơ bản
- [ ] Client tests
Files:
- internal/orchestrator/{executor,reasoner,httpclient}.go + *_test.go
```

#### Week 2: Loop tối ưu + Streaming

**Day 6-7: Agent Loop (parallel tools + straight-to-reasoner)**
```
Tasks:
- [ ] Fast-path: NeedsTools=false → 1 model stream
- [ ] Direct-tool-path: router synthesize plan → RunParallel(errgroup+semaphore) → local response hoặc straight-to-reasoner
- [ ] Executor-fallback-path: executor → RunParallel(errgroup+semaphore) → straight-to-reasoner khi router không đủ chắc
- [ ] KHÔNG loop về executor để finalize khi NeedsReasoner
- [ ] max_iterations + context cancel
- [ ] Loop tests (đếm model_round_trips)
Files:
- internal/orchestrator/loop.go, planner.go, pool.go (+ tests)
```

**Day 8: Context Manager (distill token)**
```
Tasks:
- [ ] Distill tool results (line range, không dump raw)
- [ ] BuildReasonerContext: strip scaffolding + token budget
- [ ] Tests đo token reduction
Files:
- internal/orchestrator/context.go (+ test)
```

**Day 9: HTTP handler + streaming passthrough**
```
Tasks:
- [ ] Tích hợp opt-in vào route hiện có `/v1/chat/completions` (OpenAI-compatible, stream=true); không tạo `/v1/agent/chat` ở prototype
- [ ] SSE passthrough reasoner → client
- [ ] agent_trace (model_round_trips, ttft_ms, stage_latency_ms)
- [ ] Handler tests
Files:
- internal/app/openai.go (opt-in branch) + internal/orchestrator integration tests
```

**Day 10: Integration + latency baseline**
```
Tasks:
- [ ] E2E: fast-path / direct-tool-path / complex
- [ ] Đo baseline: TTFT, p50/p99, số round-trip mỗi scenario
- [ ] Fix bug
```

**Deliverables:** prototype với read_files/list_dir/search, router local + direct tool planner, loop song song + straight-to-reasoner, streaming, trace latency. Unit 80%+, 3 integration scenario.

**Success Criteria (latency-first):**
- [ ] simple_chat: 1 RTT, TTFT < 2s
- [ ] direct tool-only rõ path: 0 model RTT
- [ ] read + reason: thường 1 model RTT
- [ ] complex fallback (read nhiều file + reason): ≤ 2 model round-trip
- [ ] đọc N file trong 1 tool turn (không đọc lẻ)
- [ ] streaming hoạt động end-to-end

---

### Phase 2: Alpha (Week 3-4)

#### Week 3: Prefetch, tool còn lại, routing hoàn chỉnh

**Day 11-12: Prefetch + edit_file + execute hard-off config**
```
Tasks:
- [ ] PrefetchAsync: khi path rõ, đọc song song với executor call
- [ ] edit_file (invalidate tool cache theo path)
- [ ] execute hard-off config + structured rejection; implementation deferred until security review
Files:
- internal/orchestrator/tools/edit_file.go, internal/orchestrator/execute_policy.go, prefetch.go
```

**Day 13-14: Classifier nâng cao + complexity estimation**
```
Tasks:
- [ ] Cải thiện rule, đo accuracy trên tập mẫu song ngữ
- [ ] Complexity estimation → quyết max_iterations, needs_reasoner
- [ ] Tests accuracy (mục tiêu >80%, đo cả ca miss keyword)
```

**Day 15: Smart routing finalize**
```
Tasks:
- [ ] Skip reasoner cho file_read/file_edit (không cần suy luận)
- [ ] Force reasoner cho code_analysis
- [ ] Routing tests
```

#### Week 4: Resilience cho tail latency

**Day 16-17: Circuit breaker + retry + fallback + HEDGING**
```
Tasks:
- [ ] Circuit breaker per upstream
- [ ] Retry backoff, timeout per-stage
- [ ] Hedged request cho reasoner (hedge_reasoner_ms) -> cải thiện p99
- [ ] Fallback executor-only khi reasoner chết
Files:
- internal/orchestrator/{circuit_breaker,retry,fallback,hedge}.go
```

**Day 18-19: Logging & tracing (per-stage latency)**
```
Tasks:
- [ ] Structured logging + trace ID propagation
- [ ] Span: router/executor_rtt/tool_exec/distill/reasoner_rtt/network
- [ ] Metrics: model_round_trips, ttft, token per stage
Files:
- internal/orchestrator/{logger,tracer,metrics}.go
```

**Day 20: Alpha testing + latency profiling**
```
Tasks:
- [ ] Profile hot path, xác nhận tool_exec ~0, RTT dominate
- [ ] Tinh chỉnh hedge_reasoner_ms theo số đo
- [ ] Bug fix + docs
```

**Deliverables:** read/list/search/edit tool path, prefetch, hedging, fallback, tracing per-stage; execute vẫn hard-off. Alpha release.

**Success Criteria:**
- [ ] Tất cả tool hoạt động, tool song song ổn định
- [ ] Intent accuracy > 80% (kèm fallback an toàn)
- [ ] Error rate < 5%
- [ ] p99 < 15s với hedging bật

---

### Phase 3: Beta (Week 5-6) — squeeze latency

#### Week 5: Caching + concurrency

**Day 21-22: Caching (tool + prefix + reasoner response)**
```
Tasks:
- [ ] Tool cache (path+mtime+size), invalidate on edit_file
- [ ] Prefix cache: cố định system+tool schema đầu prompt
- [ ] Reasoner response cache hash(query+distilled_context)
- [ ] Cache tests + đo hit rate
Files:
- internal/orchestrator/cache.go (+ test)
```

**Day 23-24: Concurrency + rate limit**
```
Tasks:
- [ ] Bounded semaphore per upstream (max_concurrent)
- [ ] Request queue, token-bucket rate limit
- [ ] Concurrency tests
Files:
- internal/orchestrator/{queue,ratelimit}.go
```

**Day 25: Performance tuning**
```
Tasks:
- [ ] Tối ưu JSON parse (streaming/incremental tool_call)
- [ ] Giảm allocation, benchmark trước/sau
- [ ] Xác nhận giảm token reasoner sau distill
```

#### Week 6: Load test + docs

**Day 26-27: Load testing (đo round-trip & TTFT dưới tải)**
```
Tasks:
- [ ] 100 concurrent, 1000 req/h
- [ ] Đo p50/p99 từng stage, model_round_trips trung bình
- [ ] Tìm + fix bottleneck (thường là reasoner_rtt)
Files:
- tests/load/{agent_test.go,scenarios.json}
```

**Day 28-29: Documentation**
```
Files:
- docs/orchestrator-{api,config,troubleshooting}.md
- docs/latency-tuning.md  <-- guide tối ưu round-trip & token
```

**Day 30: Beta release** (final test, release notes, deploy staging).

**Success Criteria:**
- [ ] 100 concurrent OK
- [ ] p99 < 12s; p50 < 3s
- [ ] Cache hit rate > 50%
- [ ] Round-trip trung bình ≤ 2 cho task phức tạp

---

### Phase 4: Production (Week 7-8)

#### Week 7: Production prep

**Day 31-32: Monitoring**
```
Tasks:
- [ ] Prometheus: latency per-stage, round-trips, ttft, hit rate
- [ ] Grafana dashboard + alert (p99, error rate, reasoner health)
- [ ] Health checks + latency regression gate trong CI
Files:
- internal/orchestrator/prometheus.go, dashboards/orchestrator.json, alerts/orchestrator.yml
```

**Day 33-34: Security review (sandbox execute)**
```
Tasks:
- [ ] Audit tool permission, sandbox scope cho execute
- [ ] Kiểm tra path traversal / command escape
Files:
- docs/security-review.md
```

**Day 35: Production config**
```
Tasks:
- [ ] config.production.json (hedge, pool, cache TTL)
- [ ] Config validation
```

#### Week 8: Launch

**Day 36-37: Deploy + smoke test + monitor.**
**Day 38-39: Gradual rollout 10% → 50% → 100%, theo dõi p99 & error rate.**
**Day 40: Handoff** (docs, runbook, knowledge transfer, post-mortem).

**Success Criteria:**
- [ ] Error rate < 1%
- [ ] p99 < 12s ổn định dưới tải production
- [ ] TTFT < 2s p50
- [ ] Feedback tích cực

---

## 4. Technical Decisions

### 4.1 Đã chốt (v2)
| Decision | Choice | Rationale |
|----------|--------|-----------|
| API integration | Existing `/v1/chat/completions` opt-in | Tận dụng auth/SSE/response format hiện có |
| Routing | LOCAL upfront | Cắt 1 model round-trip |
| Tool planning | Direct local plan first | Path rõ không cần executor RTT |
| Tool granularity | Số nhiều (read_files)+glob | Gom N file trong 1 turn |
| Tool execution | Song song (errgroup) | Giảm latency tool tổng |
| Reasoner flow | Local tools → straight-to-reasoner, ≤1 lần | Bỏ round-trip finalize |
| Streaming | Bật (SSE passthrough) | TTFT < 2s |
| Context | Distill trước reasoner | Giảm token = giảm latency |
| Tail latency | Hedged reasoner request | Cải thiện p99 (cost chấp nhận) |
| Language | Go | Codebase hiện có, performance |
| Cache/Queue/Storage | In-memory / in-memory / SQLite | Đủ cho v1 |

### 4.2 Pending
| Decision | Options | Recommendation |
|----------|---------|----------------|
| ML classifier | rule vs ML | Rule + fallback cho v1, ML khi accuracy chạm trần |
| Queue scale | in-memory vs Redis | In-memory v1, Redis khi multi-node |
| Max iterations | 3/5/10 | 5 (configurable) |
| hedge_reasoner_ms | 500/800/1200 | Đo thực tế, mặc định 800 |

---

## 5. Resource Requirements

### 5.1 Development
| Resource | Hours | Cost |
|----------|-------|------|
| Senior (review + latency gate) | 24h | $2400 |
| Developer (implementation) | 200h | $10000 |
| **Total** | **224h** | **$12400** |

### 5.2 Infrastructure (monthly)
| Resource | Cost |
|----------|------|
| Notion AI API (reasoner, có hedging) | $60–120 |
| mimo-v2.5-pro API (executor) | $20–50 |
| Monitoring | $0 (self-hosted) |
| **Total** | **$80–170/tháng** |

### 5.3 Testing (one-time)
| Resource | Cost |
|----------|------|
| Load testing | $50 |
| Security audit (sandbox) | $500 |
| **Total** | **$550** |

---

## 6. Risk Register

### 6.1 Technical
| Risk | Prob | Impact | Mitigation | Owner |
|------|------|--------|------------|-------|
| Reasoner (Notion) chậm/đổi format | Medium | High | Hedging, cache, fallback executor-only, health check | Dev |
| mimo không emit multi tool-call | Medium | Low | Direct local planner + read_files/glob; executor chỉ fallback | Dev |
| Notion không hỗ trợ SSE | Medium | Medium | Keepalive/header flush + chunk final; đo riêng header/content TTFT | Dev |
| Router misroute | Medium | Medium | Fallback an toàn + log cải thiện rule | Dev |
| Performance dưới tải | Low | High | Load test, cache, pool | Dev |

### 6.2 Operational
| Risk | Prob | Impact | Mitigation | Owner |
|------|------|--------|------------|-------|
| Cost overrun (hedging) | Medium | Low | Budget alert, hedge có điều kiện theo p99 | PM |
| Timeline trượt | Medium | Medium | Buffer, cắt scope (execute/ML để sau) | PM |

---

## 7. Success Metrics

### 7.1 Technical (latency-first)
| Metric | Target | Measurement |
|--------|--------|-------------|
| TTFT (p50) | < 2s | Trace |
| Latency (p50) | < 3s | Prometheus |
| Latency (p99) | < 12s | Prometheus |
| Model round-trips (complex) | ≤ 2 | agent_trace |
| Error rate | < 2% | Logs |
| Cache hit rate | > 50% | Prometheus |
| Tool success rate | > 95% | Logs |

### 7.2 Business
| Metric | Target | Measurement |
|--------|--------|-------------|
| User satisfaction | > 4/5 | Survey |
| API adoption | 100 users | Analytics |
| Cost per request | < $0.01 | Billing |

---

## 8. Communication Plan
| Meeting | Frequency | Attendees |
|---------|-----------|-----------|
| Daily standup | Daily | Dev, PM |
| Architecture/latency review | Weekly | Dev, Senior |
| Sprint review | Bi-weekly | All |

---

## 9. Appendix

### 9.1 Glossary
| Term | Definition |
|------|------------|
| Executor | Model gom context qua tool calls (mimo-v2.5-pro) |
| Reasoner | Model suy luận trên context đã chắt lọc (Notion AI) |
| Round-trip | Một lần gọi model từ xa (đơn vị latency chính) |
| Fast-path | Đường đi không cần tool, 1 RTT |
| Distill | Chắt lọc context để giảm token reasoner |
| Prefetch | Đọc tool song song trước khi executor yêu cầu |
| Hedging | Gửi request reasoner thứ 2 để cắt tail latency |

### 9.2 References
- OpenAI Function Calling — https://platform.openai.com/docs/guides/function-calling
- Go net/http connection pooling, errgroup

### 9.3 Open Questions for Senior
1. Notion endpoint hỗ trợ streaming SSE? Nếu không, đo header/content TTFT riêng.
2. mimo emit multi tool-call/lượt ổn định? Không blocker nhờ direct local planner. mimo có prefix cache?
3. hedge_reasoner_ms tối ưu? Ngân sách reasoner cho hedging?
4. Sandbox scope cho execute (giữ default-off v1)?
5. 8 tuần có aggressive? Cắt scope nào nếu trượt (ML classifier / execute)?

---

## 10. Approval
| Role | Name | Date | Signature |
|------|------|------|-----------|
| Senior Engineer | | | |
| Product Manager | | | |
| Tech Lead | | | |
