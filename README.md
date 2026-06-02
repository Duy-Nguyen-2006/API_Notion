# Notion2API - Fork with Tool Calls Support

Một bridge service mã nguồn mở, viết bằng Go, chuyển đổi Notion AI thành API tương thích OpenAI. Fork này bổ sung hỗ trợ **tool_calls** (function calling) ở cấp gateway, cho phép tích hợp với các AI agent như Droid, Cursor, Windsurf, và các công cụ hỗ trợ OpenAI function calling khác.

## Tính năng chính

### API tương thích OpenAI
- `/v1/models` - Danh sách models có sẵn
- `/v1/chat/completions` - Chat completions (hỗ trợ streaming)
- `/v1/responses` - Responses API
- `/healthz` - Health check

### Models được hỗ trợ
| Model ID | Codename Notion | Provider | Ghi chú |
|----------|----------------|----------|---------|
| `auto` | (tự động) | System | Mặc định |
| `opus-4.8` | `ambrosia-tart-high` | Anthropic | Mới nhất |
| `opus-4.7` | `apricot-sorbet-medium` | Anthropic | |
| `opus-4.6` | `avocado-froyo-medium` | Anthropic | |
| `gpt-5.5` | `opal-quince-medium` | OpenAI | Mới nhất |
| `gpt-5.4` | `oval-kumquat-medium` | OpenAI | |
| `gpt-5.2` | `oatmeal-cookie` | OpenAI | |
| `grok-4.3` | `xigua-mochi-medium` | xAI | Mới nhất |
| `sonnet-4.6` | `almond-croissant-low` | Anthropic | |
| `haiku-4.5` | `anthropic-haiku-4.5` | Anthropic | |
| `gemini-3.1-pro` | `galette-medium-thinking` | Google | |
| `gemini-2.5-flash` | `vertex-gemini-2.5-flash` | Google | |
| `gemini-3-flash` | `gingerbread` | Google | |
| `minimax-m2.5` | `fireworks-minimax-m2.5` | MiniMax | |

### Tool Calls (Function Calling)
Fork này bổ sung hỗ trợ tool_calls ở cấp gateway:
- **Deterministic synthesis**: Gateway tự tạo tool_call từ prompt client
- **Hỗ trợ các tool phổ biến**: `read_file`, `edit_file`, `grep`, `execute_command`, v.v.
- **Generic argument extraction**: Tự động extract arguments từ schema
- **Tool result handling**: Xử lý kết quả tool và gửi cho Notion để tạo response cuối
- **Multi-turn tool loop**: Hỗ trợ nhiều vòng gọi tool liên tiếp

### Quản lý
- WebUI quản trị tại `/admin`
- Multi-account pool với load balancing
- Session persistence qua SQLite
- Cookie-based authentication

## Cài đặt

### Yêu cầu
- Go 1.25.0+ (nếu build từ source)
- Notion account với AI access
- Browser cookies từ Notion

### Build từ source

```bash
# Clone repository
git clone https://github.com/YOUR_USERNAME/notion2api-fork.git
cd notion2api-fork

# Build
go build -o notion2api ./cmd/notion2api/

# Chạy
./notion2api --config ./config.example.json
```

### Cấu hình

Tạo file `config.json` từ mẫu:

```json
{
  "host": "127.0.0.1",
  "port": 8787,
  "api_key": "YOUR_API_KEY",
  "admin": {
    "enabled": true,
    "password": "YOUR_ADMIN_PASSWORD"
  },
  "probe_json": "probe_files/default/probe.json",
  "model_id": "auto",
  "features": {
    "use_web_search": true,
    "enable_generate_image": true
  }
}
```

### Lấy Notion Cookies

1. Đăng nhập vào Notion trong trình duyệt
2. Sử dụng script `convert_cookies.sh` để chuyển đổi cookies:
   ```bash
   ./convert_cookies.sh cookies-export.json
   ```
3. Copy kết quả vào `probe_files/default/probe.json`

## Sử dụng

### Chat completions

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "opus-4.8",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": false
  }'
```

### Tool calls (Function calling)

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "opus-4.8",
    "messages": [{"role": "user", "content": "Read /tmp/test.txt"}],
    "tools": [{
      "type": "function",
      "function": {
        "name": "read_file",
        "description": "Read a local file",
        "parameters": {
          "type": "object",
          "properties": {
            "file_path": {"type": "string"}
          },
          "required": ["file_path"]
        }
      }
    }],
    "tool_choice": "auto",
    "stream": false
  }'
```

Response sẽ chứa `tool_calls` với arguments được extract tự động:

```json
{
  "choices": [{
    "finish_reason": "tool_calls",
    "message": {
      "role": "assistant",
      "tool_calls": [{
        "id": "call_0",
        "type": "function",
        "function": {
          "name": "read_file",
          "arguments": "{\"file_path\":\"/tmp/test.txt\"}"
        }
      }]
    }
  }]
}
```

### Tool result follow-up

Sau khi nhận tool_calls, client thực thi tool và gửi kết quả lại:

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "opus-4.8",
    "messages": [
      {"role": "user", "content": "Read /tmp/test.txt"},
      {"role": "assistant", "content": null, "tool_calls": [...]},
      {"role": "tool", "tool_call_id": "call_0", "content": "file content here"}
    ],
    "tools": [...],
    "stream": false
  }'
```

## Deploy với Cloudflare Tunnel

Để expose API ra internet qua Cloudflare:

```bash
# Cài cloudflared
# Đăng nhập Cloudflare
cloudflared tunnel login

# Tạo tunnel
cloudflared tunnel create notion-api

# Cấu hình tunnel
cat > ~/.cloudflared/config.yml << EOF
tunnel: YOUR_TUNNEL_ID
credentials-file: /home/user/.cloudflared/YOUR_TUNNEL_ID.json
ingress:
  - hostname: notion.yourdomain.com
    service: http://127.0.0.1:8787
  - service: http_status:404
EOF

# Tạo DNS record
cloudflared tunnel route dns notion-api notion.yourdomain.com

# Chạy tunnel
cloudflared tunnel run notion-api
```

## Docker

```bash
# Sử dụng docker-compose
docker compose up -d --build

# Hoặc build manual
docker build -t notion2api .
docker run -p 8787:8787 -v ./config.json:/app/config.json notion2api
```

## Phát triển

### Cấu trúc project

```
notion2api-fork/
├── cmd/notion2api/          # Entry point
├── internal/app/            # Core application
│   ├── main.go              # HTTP handlers
│   ├── models.go            # Model registry
│   ├── tool_calls.go        # Tool calls middleware
│   ├── notion_client.go     # Notion API client
│   ├── openai_types.go      # OpenAI request/response types
│   └── ...
├── static/admin/            # WebUI assets
├── config.example.json      # Example config
└── README.md
```

### Chạy tests

```bash
go test ./...
```

### Thêm models mới

1. Tìm codename Notion bằng cách capture network request trong Notion web
2. Thêm vào `builtinModelDefinitions()` trong `models.go`:
   ```go
   {ID: "model-id", Name: "Model Name", NotionModel: "notion-codename", Family: "provider", Group: "group", Enabled: true, Aliases: []string{"alias1", "alias2"}},
   ```
3. Build lại và test

## So sánh với upstream

Fork này dựa trên [GALIAIS/Notion2API](https://github.com/GALIAIS/Notion2API) với các bổ sung:

- ✅ Tool calls (function calling) support
- ✅ Thêm models mới: opus-4.8, gpt-5.5, grok-4.3
- ✅ Generic argument extraction cho Droid-style tools
- ✅ Tool result handling và multi-turn support
- ✅ Preserve probe workspace ID (không bị override)

## License

MIT License - Xem file `LICENSE` để biết chi tiết.

## Credits

- Gốc: [GALIAIS/Notion2API](https://github.com/GALIAIS/Notion2API)
- Fork với tool calls support bởi community

## Hỗ trợ

- Issues: GitHub Issues
- Discussions: GitHub Discussions

---

**Lưu ý**: Đây là công cụ reverse-engineered, sử dụng Notion AI không chính thức. Sử dụng có trách nhiệm và tuân thủ Terms of Service của Notion.
