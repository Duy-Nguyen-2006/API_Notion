# Runbook: Orchestrator rollout (10% → 50% → 100%)

## Preconditions

- `NOTION_API_KEY` / `MIMO_API_KEY` smoke PASS: `./scripts/smoke-endpoints.sh`
- CI gate PASS: `./scripts/ci-latency-gate.sh`
- `config.production.json` + env: `N2A_API_KEY`, `N2A_ADMIN_PASSWORD`
- Account `probe_json` paths exist on host (not stale `NotionAPI/` paths in SQLite)
- `execute_enabled: false` in prod unless security sign-off

## Deploy

1. Build: `go build -o notion2api ./cmd/notion2api/`
2. Restart service (systemd/docker) with `config.production.json`
3. Verify: `curl -s http://127.0.0.1:8787/healthz` → 200
4. Verify public tunnel: `curl -s https://notion.lgmmo.click/healthz` → 200

## Opt-in traffic

Orchestrator activates when:

- `model: "auto"`, or
- `agent_config.enabled: true` on `/v1/chat/completions`

Rollout = increase share of clients using `auto` / agent_config (feature flag or client config).

| Stage | Action | Watch (24–48h) |
|-------|--------|----------------|
| **10%** | Enable for one internal client / canary | `notion2api_orchestrator_*` metrics, error rate, `agent_trace.ttft_ms` |
| **50%** | Half of agent traffic on `auto` | p50/p99 latency, reasoner 502 rate |
| **100%** | Default agent path on orchestrator | error &lt; 1%, p99 &lt; 12s |

## Rollback

- Point clients back to fixed model IDs (`opus-4.8`, etc.) without `agent_config`
- Restart previous binary if regression
- Check account pool: `no usable accounts` → fix SQLite `probe_json` paths

## Keys (do not commit)

| Service | URL | Key env |
|---------|-----|---------|
| Notion bridge | `notion.lgmmo.click` | `NOTION_API_KEY` (gateway `api_key`) |
| Mimo executor | `api.lgmmo.click` | `MIMO_API_KEY` (separate gateway key) |

## Post-launch

- 7d window: p99, error rate, cost/request
- Tune `hedge_reasoner_ms` per `docs/latency-tuning.md`
- Backlog: wire external mimo `ExecutorClient` for executor-fallback path (today reasoner uses Notion `appModelClient` only)
