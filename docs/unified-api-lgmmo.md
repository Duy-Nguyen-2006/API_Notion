# Unified `api.lgmmo.click` (mimo + Notion2API)

## Hiện trạng (VPS `100.65.252.7`)

| Thành phần | Port | Domain |
|------------|------|--------|
| **cli-proxy-api** (`/root/cliproxyapi`) | **8317** | `api.lgmmo.click` (Caddy → `host.docker.internal:8317`) |
| **notion2api** | **8787** | Máy dev / `notion.lgmmo.click` (tunnel riêng) |

Caddy (`n8n-caddy`) **không đọc được `model` trong body** → cần **gateway ứng dụng** (hoặc đổi `api.lgmmo.click` trỏ gateway thay vì thẳng 8317).

## Mục tiêu

Một base URL cho client:

```text
https://api.lgmmo.click/v1/chat/completions
```

| `model` (ví dụ) | Backend |
|-----------------|---------|
| `mimo-v2.5-pro` | `127.0.0.1:8317` (cliproxy) |
| `opus-4.8`, `gpt-5.5`, `auto`, … | `127.0.0.1:8787` (notion2api) hoặc tunnel `notion.lgmmo.click` |

Orchestrator (`auto`, `agent_config`) chạy trên **notion2api**; executor mimo có thể gọi `http://127.0.0.1:8317` nội bộ VPS sau này.

---

## Hướng triển khai (khuyến nghị)

### A) Gateway trên VPS + notion2api cùng máy (một miền, ít phụ thuộc PC)

1. **Build & deploy notion2api** lên VPS (systemd, port `8787`).
2. Copy **`probe_files/`** + `data/notion2api.sqlite` (hoặc login admin) — session Notion phải chạy được **từ IP VPS**.
3. Chạy **`lgmmo-gateway`** (hoặc `notion2api` fork nhỏ) listen **8300**:
   - Parse `POST /v1/chat/completions` → field `model`
   - Nếu model ∈ danh sách Notion → reverse proxy `http://127.0.0.1:8787`
   - Nếu `mimo-*` / cliproxy models → `http://127.0.0.1:8317`
   - `GET /v1/models` → merge JSON từ cả hai (hoặc static list)
4. Sửa Caddy:

```caddyfile
api.lgmmo.click {
    reverse_proxy host.docker.internal:8300
    # ... CORS như hiện tại
}
```

5. **Auth**: một `api_key` phía client hoặc hai key — gateway strip/replace `Authorization` khi forward (Notion `Dyu123@as`, mimo `Dyuchan123@as`).

**Ưu:** latency ổn, không cần PC bật.  
**Nhược:** Notion cookies trên VPS; refresh session trên server.

### B) Gateway trên VPS, Notion vẫn ở nhà (tunnel)

Giữ notion2api trên PC + `notion.lgmmo.click` tunnel.

Gateway trên VPS:

- `mimo-v2.5-pro` → `127.0.0.1:8317`
- Notion models → `https://notion.lgmmo.click` (upstream)

**Ưu:** không di chuyển cookies.  
**Nhược:** PC/tunnel phải online; thêm hop; `auto` orchestrator chạy trên nhà không phải VPS.

### C) Chỉ deploy notion2api lên VPS, tách path (không khuyến nghị cho OpenAI client)

Ví dụ `/v1/notion/chat/completions` vs `/v1/mimo/...` — nhiều client không hỗ trợ.

---

## Deploy notion2api lên VPS (tóm tắt lệnh)

Trên **máy dev** (không đưa `config.json` có secret lên git):

```bash
GOOS=linux GOARCH=amd64 go build -o notion2api-linux ./cmd/notion2api/
rsync -avz notion2api-linux config.production.json root@100.65.252.7:/opt/notion2api/
rsync -avz probe_files/ root@100.65.252.7:/opt/notion2api/probe_files/
```

Trên **VPS**:

```bash
mkdir -p /opt/notion2api/data
# env: N2A_API_KEY, N2A_ADMIN_PASSWORD
# systemd unit: WorkingDirectory=/opt/notion2api, ExecStart=/opt/notion2api/notion2api-linux --config config.production.json
# host 0.0.0.0 port 8787 → chỉ listen 127.0.0.1:8787 nếu chỉ gateway gọi
```

Kiểm tra: `curl -s http://127.0.0.1:8787/healthz`

---

## Routing theo model (logic gateway)

```text
notionModels = { auto, opus-4.8, opus-4.7, gpt-5.5, ... }  # từ models.go
if model in notionModels or strings.HasPrefix(model, "opus-") ...
    → upstream notion2api
else if model == "mimo-v2.5-pro" or strings.HasPrefix(model, "mimo-")
    → upstream cliproxy 8317
else
    → 400 unknown model hoặc fallback cliproxy
```

`GET /v1/models`: proxy notion + merge mimo list (hoặc trả union cố định).

---

## Bước tiếp theo trong repo

1. Thêm `cmd/lgmmo-gateway/` (reverse proxy ~200 dòng Go) **hoặc** config cliproxy nếu có upstream-by-model.
2. `deploy/vps/notion2api.service` + `deploy/vps/lgmmo-gateway.service`
3. Patch Caddy trên VPS (backup trước khi reload).
4. Cập nhật `scripts/smoke-endpoints.sh`: `NOTION_URL` và `MIMO_URL` cùng `https://api.lgmmo.click/v1/chat/completions` với model khác nhau.

---

## Không nên

- Commit `probe.json`, `config.json`, password SSH vào git.
- Expose `8787` public nếu đã có gateway — chỉ bind `127.0.0.1`.
