# PLAN: Implementation Orchestrator-Reasoner

## 1. Executive Summary

**Goal**: Implement hybrid architecture where mimo-v2.5-pro (executor) handles tool calls và Notion AI (reasoner) handles reasoning.

**Timeline**: 8 weeks (2 weeks prototype → 2 weeks alpha → 2 weeks beta → 2 weeks production)

**Effort**: ~200-300 hours

**Risk**: Medium-High (complex architecture, multiple integration points)

## 2. Prerequisites

### 2.1 Infrastructure
- [x] Notion AI API (notion.lgmmo.click) - đang hoạt động
- [x] mimo-v2.5-pro API (api.lgmmo.click) - đang hoạt động
- [x] Go development environment
- [ ] Monitoring/Logging infrastructure
- [ ] Load testing tools

### 2.2 Knowledge
- [x] OpenAI API format
- [x] Notion API quirks
- [x] Go concurrency patterns
- [ ] Agent architecture patterns (need research)
- [ ] Intent classification (need research)

### 2.3 Resources
- 1 Senior Engineer (review architecture)
- 1 Developer (implementation)
- Budget for API calls (~$50-100/month testing)

## 3. Detailed Implementation Plan

### Phase 1: Prototype (Week 1-2)

#### Week 1: Foundation

**Day 1-2: Architecture Setup**
```
Tasks:
- [ ] Create new package: internal/orchestrator/
- [ ] Define interfaces: Agent, Tool, ModelClient
- [ ] Implement basic AgentLoopController
- [ ] Add unit tests skeleton

Files:
- internal/orchestrator/agent.go
- internal/orchestrator/tool.go
- internal/orchestrator/model.go
- internal/orchestrator/loop.go
- internal/orchestrator/loop_test.go
```

**Day 3-4: Tool Registry**
```
Tasks:
- [ ] Implement Tool interface
- [ ] Create ReadFileTool
- [ ] Create ListDirTool
- [ ] Create SearchTool
- [ ] Add tool tests

Files:
- internal/orchestrator/tools/registry.go
- internal/orchestrator/tools/read_file.go
- internal/orchestrator/tools/list_dir.go
- internal/orchestrator/tools/search.go
- internal/orchestrator/tools/*_test.go
```

**Day 5: Model Clients**
```
Tasks:
- [ ] Implement ExecutorClient (calls mimo-v2.5-pro)
- [ ] Implement ReasonerClient (calls Notion AI)
- [ ] Add retry/timeout logic
- [ ] Add client tests

Files:
- internal/orchestrator/executor.go
- internal/orchestrator/reasoner.go
- internal/orchestrator/executor_test.go
- internal/orchestrator/reasoner_test.go
```

#### Week 2: Integration

**Day 6-7: Agent Loop**
```
Tasks:
- [ ] Implement full agent loop
- [ ] Add tool call handling
- [ ] Add reasoning threshold logic
- [ ] Add loop tests

Files:
- internal/orchestrator/loop.go (update)
- internal/orchestrator/loop_test.go (update)
```

**Day 8-9: HTTP Handler**
```
Tasks:
- [ ] Create /v1/agent/chat endpoint
- [ ] Parse agent_config from request
- [ ] Format response with agent_trace
- [ ] Add handler tests

Files:
- internal/app/agent_handler.go
- internal/app/agent_handler_test.go
```

**Day 10: Integration Test**
```
Tasks:
- [ ] End-to-end test: simple chat
- [ ] End-to-end test: tool call
- [ ] End-to-end test: reasoning
- [ ] Fix bugs found
```

**Deliverables:**
- Working prototype with 3 tools
- Basic agent loop
- Unit tests (80%+ coverage)
- Integration tests (3 scenarios)

**Success Criteria:**
- [ ] Can read file via tool call
- [ ] Can list directory via tool call
- [ ] Can handle simple chat
- [ ] Latency < 10s for simple requests

---

### Phase 2: Alpha (Week 3-4)

#### Week 3: Enhanced Tools

**Day 11-12: More Tools**
```
Tasks:
- [ ] Implement EditFileTool
- [ ] Implement ExecuteCommandTool (sandboxed)
- [ ] Implement GrepTool
- [ ] Add tool tests

Files:
- internal/orchestrator/tools/edit_file.go
- internal/orchestrator/tools/execute.go
- internal/orchestrator/tools/grep.go
```

**Day 13-14: Intent Classifier**
```
Tasks:
- [ ] Implement rule-based classifier
- [ ] Add pattern matching
- [ ] Add complexity estimation
- [ ] Add classifier tests

Files:
- internal/orchestrator/classifier.go
- internal/orchestrator/classifier_test.go
- internal/orchestrator/patterns.json
```

**Day 15: Smart Routing**
```
Tasks:
- [ ] Route based on intent
- [ ] Skip tools for simple chat
- [ ] Force tools for file operations
- [ ] Add routing tests

Files:
- internal/orchestrator/router.go
- internal/orchestrator/router_test.go
```

#### Week 4: Error Handling

**Day 16-17: Error Handling**
```
Tasks:
- [ ] Implement circuit breaker
- [ ] Add retry with backoff
- [ ] Add timeout handling
- [ ] Add fallback logic
- [ ] Add error tests

Files:
- internal/orchestrator/circuit_breaker.go
- internal/orchestrator/retry.go
- internal/orchestrator/fallback.go
```

**Day 18-19: Logging & Tracing**
```
Tasks:
- [ ] Add structured logging
- [ ] Add trace ID propagation
- [ ] Add metrics collection
- [ ] Add logging tests

Files:
- internal/orchestrator/logger.go
- internal/orchestrator/tracer.go
- internal/orchestrator/metrics.go
```

**Day 20: Alpha Testing**
```
Tasks:
- [ ] Manual testing all scenarios
- [ ] Performance profiling
- [ ] Bug fixes
- [ ] Documentation updates
```

**Deliverables:**
- All file tools implemented
- Intent classifier
- Error handling
- Logging/Tracing
- Alpha release

**Success Criteria:**
- [ ] All 6 tools working
- [ ] Intent classification accurate >80%
- [ ] Error rate <5%
- [ ] Latency <15s for complex requests

---

### Phase 3: Beta (Week 5-6)

#### Week 5: Optimization

**Day 21-22: Caching**
```
Tasks:
- [ ] Cache tool results
- [ ] Cache model responses
- [ ] Implement cache invalidation
- [ ] Add cache tests

Files:
- internal/orchestrator/cache.go
- internal/orchestrator/cache_test.go
```

**Day 23-24: Concurrency**
```
Tasks:
- [ ] Parallel tool execution
- [ ] Request queuing
- [ ] Rate limiting
- [ ] Add concurrency tests

Files:
- internal/orchestrator/pool.go
- internal/orchestrator/queue.go
- internal/orchestrator/ratelimit.go
```

**Day 25: Performance Tuning**
```
Tasks:
- [ ] Profile hot paths
- [ ] Optimize JSON parsing
- [ ] Reduce allocations
- [ ] Benchmark improvements
```

#### Week 6: Testing & Docs

**Day 26-27: Load Testing**
```
Tasks:
- [ ] Create load test scripts
- [ ] Test 100 concurrent requests
- [ ] Test 1000 requests/hour
- [ ] Identify bottlenecks
- [ ] Fix performance issues

Files:
- tests/load/agent_test.go
- tests/load/scenarios.json
```

**Day 28-29: Documentation**
```
Tasks:
- [ ] API documentation
- [ ] Configuration guide
- [ ] Troubleshooting guide
- [ ] Architecture diagram

Files:
- docs/orchestrator-api.md
- docs/orchestrator-config.md
- docs/orchestrator-troubleshooting.md
```

**Day 30: Beta Release**
```
Tasks:
- [ ] Final testing
- [ ] Release notes
- [ ] Deploy to staging
- [ ] Notify stakeholders
```

**Deliverables:**
- Caching layer
- Concurrency support
- Load test results
- Documentation
- Beta release

**Success Criteria:**
- [ ] 100 concurrent requests handled
- [ ] 99th percentile latency <20s
- [ ] Error rate <2%
- [ ] Cache hit rate >50%

---

### Phase 4: Production (Week 7-8)

#### Week 7: Production Prep

**Day 31-32: Monitoring**
```
Tasks:
- [ ] Set up Prometheus metrics
- [ ] Create Grafana dashboards
- [ ] Set up alerts
- [ ] Add health checks

Files:
- internal/orchestrator/prometheus.go
- dashboards/orchestrator.json
- alerts/orchestrator.yml
```

**Day 33-34: Security Review**
```
Tasks:
- [ ] Audit tool permissions
- [ ] Review sandbox config
- [ ] Check API key handling
- [ ] Penetration testing

Files:
- docs/security-review.md
```

**Day 35: Production Config**
```
Tasks:
- [ ] Production config template
- [ ] Environment variables
- [ ] Secrets management
- [ ] Config validation

Files:
- config.production.json
- config.docker.json (update)
```

#### Week 8: Launch

**Day 36-37: Deployment**
```
Tasks:
- [ ] Deploy to production
- [ ] Smoke testing
- [ ] Monitor metrics
- [ ] Fix production issues
```

**Day 38-39: Gradual Rollout**
```
Tasks:
- [ ] 10% traffic
- [ ] Monitor error rates
- [ ] 50% traffic
- [ ] Monitor performance
- [ ] 100% traffic
```

**Day 40: Handoff**
```
Tasks:
- [ ] Final documentation
- [ ] Knowledge transfer
- [ ] Runbook creation
- [ ] Post-mortem
```

**Deliverables:**
- Production deployment
- Monitoring dashboards
- Security audit
- Runbook
- Post-mortem

**Success Criteria:**
- [ ] 99.9% uptime
- [ ] Error rate <1%
- [ ] 99th percentile latency <15s
- [ ] Positive user feedback

---

## 4. Technical Decisions

### 4.1 Decisions Made

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Language | Go | Existing codebase, performance |
| Intent Classifier | Rule-based | Simpler, faster, debuggable |
| Tool Execution | Sync | Simpler, predictable |
| Caching | In-memory | Fast, simple for v1 |
| Storage | SQLite | Existing, sufficient |

### 4.2 Decisions Pending

| Decision | Options | Recommendation |
|----------|---------|----------------|
| Queue | In-memory vs Redis | In-memory for v1, Redis for scale |
| Rate Limiting | Token bucket vs Sliding window | Token bucket |
| Cache TTL | 5min vs 30min vs 1hr | 5min for tool results, 30min for responses |
| Max Iterations | 3 vs 5 vs 10 | 5 (configurable) |

---

## 5. Resource Requirements

### 5.1 Development

| Resource | Hours | Cost |
|----------|-------|------|
| Senior Engineer (review) | 20h | $2000 |
| Developer (implementation) | 180h | $9000 |
| **Total** | **200h** | **$11000** |

### 5.2 Infrastructure

| Resource | Monthly Cost |
|----------|--------------|
| Notion AI API | $50-100 |
| mimo-v2.5-pro API | $20-50 |
| Monitoring | $0 (self-hosted) |
| **Total** | **$70-150/month** |

### 5.3 Testing

| Resource | Cost |
|----------|------|
| Load testing | $50 (one-time) |
| Security audit | $500 (one-time) |
| **Total** | **$550** |

---

## 6. Risk Register

### 6.1 Technical Risks

| Risk | Probability | Impact | Mitigation | Owner |
|------|-------------|--------|------------|-------|
| Model API instability | Medium | High | Circuit breaker, fallback | Dev |
| Tool execution bugs | Medium | Medium | Extensive testing | Dev |
| Performance issues | Low | High | Load testing, optimization | Dev |
| Security vulnerabilities | Low | High | Security review, sandbox | Senior |

### 6.2 Operational Risks

| Risk | Probability | Impact | Mitigation | Owner |
|------|-------------|--------|------------|-------|
| Cost overrun | Medium | Medium | Budget alerts, rate limiting | PM |
| Delayed timeline | Medium | Medium | Buffer time, scope reduction | PM |
| Knowledge gaps | Low | Medium | Documentation, training | Senior |

---

## 7. Success Metrics

### 7.1 Technical Metrics

| Metric | Target | Measurement |
|--------|--------|-------------|
| Latency (p50) | <5s | Prometheus |
| Latency (p99) | <15s | Prometheus |
| Error rate | <2% | Logs |
| Cache hit rate | >50% | Prometheus |
| Tool success rate | >95% | Logs |

### 7.2 Business Metrics

| Metric | Target | Measurement |
|--------|--------|-------------|
| User satisfaction | >4/5 | Survey |
| API adoption | 100 users | Analytics |
| Cost per request | <$0.01 | Billing |

---

## 8. Communication Plan

### 8.1 Stakeholders

| Stakeholder | Role | Communication |
|-------------|------|---------------|
| Senior Engineer | Architecture review | Weekly sync |
| Product Manager | Requirements, priorities | Daily standup |
| Users | Feedback | GitHub issues |

### 8.2 Meetings

| Meeting | Frequency | Attendees |
|---------|-----------|-----------|
| Daily standup | Daily | Dev, PM |
| Architecture review | Weekly | Dev, Senior |
| Sprint review | Bi-weekly | All |
| Retrospective | Monthly | All |

---

## 9. Appendix

### 9.1 Glossary

| Term | Definition |
|------|------------|
| Executor | Model that handles tool calls (mimo-v2.5-pro) |
| Reasoner | Model that handles reasoning (Notion AI) |
| Agent Loop | Iterative process of tool calls and reasoning |
| Intent | User's goal (file_read, code_analysis, etc.) |

### 9.2 References

- [OpenAI Function Calling](https://platform.openai.com/docs/guides/function-calling)
- [LangChain Agent Architecture](https://docs.langchain.com/docs/)
- [Notion API Documentation](https://developers.notion.com/)

### 9.3 Open Questions for Senior

1. **Architecture**: Single agent loop hay multi-agent parallel?
2. **Intent Classifier**: Rule-based đủ không, hay cần ML?
3. **Tool Execution**: Sandboxing scope như thế nào?
4. **Cost**: Budget approved chưa?
5. **Timeline**: 8 weeks có aggressive quá không?
6. **Merge Strategy**: Merge vào api.lgmmo.click thế nào?

---

## 10. Approval

| Role | Name | Date | Signature |
|------|------|------|-----------|
| Senior Engineer | | | |
| Product Manager | | | |
| Tech Lead | | | |
