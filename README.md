# M-Tunnel

M-Tunnel là reverse TCP tunnel cá nhân cho SQL Server/ERP. Agent trên máy SQL kết nối outbound tới Gateway trên VPS; người dùng vẫn connect SQL Server bình thường bằng `VPS_IP,PublicPort` hoặc `domain,PublicPort`.

## 1. M-Tunnel là gì

MVP này thay thế FRP ở nhu cầu expose SQL Server sau NAT/IP động:

- Máy SQL không mở port modem/router.
- Gateway trên VPS mở WebSocket agent port và dải SQL public port.
- Agent chủ động giữ kết nối outbound tới Gateway.
- Mỗi khách hàng có `client_id`, token và `public_port` riêng.

## 2. Mô hình hoạt động

```text
ERP/SSMS TCP -> VPS:12601 -> M-Tunnel Gateway -> WebSocket -> M-Tunnel Agent -> 127.0.0.1:1433
```

Gateway nhận TCP ở `public_port`, gửi `OPEN_STREAM` cho đúng Agent, rồi forward DATA hai chiều qua WebSocket binary frame.

## 3. Cài Gateway bằng Docker

Sửa `configs/gateway.yaml`: bật dashboard, đặt bcrypt hash cho admin (xem mục 11), cấu hình `port_range`.

```bash
cd docker
docker compose up -d --build
```

Ports mặc định:

```text
8080/tcp   dashboard (giới hạn bằng allowed_ips hoặc reverse proxy)
8443/tcp   agent websocket
12600-12799/tcp   public SQL ports
```

Clients được lưu trong SQLite (`/app/data/gateway.db`, mount qua volume `gateway-data`). Thêm/sửa/xóa client qua dashboard, không cần sửa YAML hay restart container.

### Workflow git push + docker pull (server Linux)

Trên máy dev:

```bash
git add -A && git commit -m "deploy" && git push
```

Trên server Linux (đã clone repo):

```bash
git pull
cd docker
docker compose up -d --build
```

Hoặc nếu push image lên registry:

```bash
docker compose pull && docker compose up -d
```

Data clients trong volume `gateway-data` được giữ nguyên qua các lần redeploy.


## 4. Cài Gateway bằng systemd

Build binary:

```bash
go build -o bin/m-tunnel-gateway ./cmd/gateway
```

Cài service:

```bash
bash scripts/install-gateway-systemd.sh ./bin/m-tunnel-gateway ./configs/gateway.yaml
```

Xem log:

```bash
journalctl -u m-tunnel-gateway -f
```

Gỡ service:

```bash
bash scripts/uninstall-gateway-systemd.sh
```

## 5. Cài Agent trên Windows

Build Agent:

```powershell
go build -o bin\m-tunnel-agent.exe ./cmd/agent
```

Chạy console để debug:

```powershell
.\bin\m-tunnel-agent.exe -config .\configs\agent.yaml
```

Cài Windows Service:

```powershell
.\scripts\install-agent-service.ps1 -BinaryPath .\bin\m-tunnel-agent.exe -ConfigPath .\configs\agent.yaml
```

Gỡ service:

```powershell
.\scripts\uninstall-agent-service.ps1 -BinaryPath .\bin\m-tunnel-agent.exe -ConfigPath .\configs\agent.yaml
```

## 6. Cấu hình SQL Server

Trên máy chạy Agent:

- SQL Server lắng nghe TCP `127.0.0.1:1433` hoặc port nội bộ bạn chọn.
- `configs/agent.yaml` đặt `local_addr: "127.0.0.1:1433"`.
- Bật SQL Server TCP/IP trong SQL Server Configuration Manager nếu cần.
- Firewall local phải cho Agent connect tới SQL local.

## 7. Mở firewall VPS

Mở inbound TCP:

```text
8443
12600-12799
```

Nếu dùng Caddy/Nginx TLS reverse proxy, WebSocket có thể đi qua HTTPS/WSS, nhưng public SQL ports vẫn là TCP riêng.

## 8. Cách connect SSMS

Ví dụ KH001 dùng public port `12601`:

```text
VPS_IP,12601
```

Hoặc domain:

```text
tunnel.example.com,12601
```

Login SQL như bình thường. M-Tunnel không đọc hoặc log dữ liệu SQL/password.

## 9. Troubleshooting

- `auth failed`: kiểm tra `client_id`, token và token dài tối thiểu 32 ký tự.
- `client offline`: Agent chưa connect hoặc vừa mất mạng.
- `public connection rejected by IP whitelist`: IP người dùng không có trong `allowed_ips`.
- `local sql connect failed`: Agent không connect được `local_addr`; kiểm tra SQL Server, TCP/IP và firewall local.
- Không connect được VPS port: kiểm tra cloud firewall, OS firewall, Docker port mapping hoặc systemd service.
- Xem log Gateway: `journalctl -u m-tunnel-gateway -f` hoặc `docker logs -f m-tunnel-gateway`.
- Xem log Agent: console output hoặc `%ProgramData%\M-Tunnel\agent.log`.

## 10. Dashboard quản trị

Bật dashboard trong `configs/gateway.yaml`:

```yaml
dashboard:
  enabled: true
  listen_addr: "0.0.0.0:8080"
  basic_auth_user: "admin"
  basic_auth_hash: "$2a$10$..."
  allowed_ips: []
```

Tạo bcrypt hash cho mật khẩu admin:

```bash
go build -o bin/m-tunnel-hashpw ./cmd/hashpw
./bin/m-tunnel-hashpw your-password
```

Dashboard có:

- List clients
- Add client mới
- Auto-allocate public port từ `port_range`
- Regenerate token
- Delete client
- Xem online/offline + traffic

URL mặc định:

```text
http://VPS_IP:8080/
```

Khuyến nghị để dashboard sau reverse proxy HTTPS hoặc ít nhất giới hạn `allowed_ips`.

## 11. Security checklist

- Token client do dashboard tự gen 48 ký tự base64url.
- Không commit token thật hoặc file `gateway.db`.
- Dùng `allowed_ips` cho từng `public_port` khi có thể.
- Dashboard: bcrypt hash mật khẩu, giới hạn `allowed_ips`, ưu tiên reverse proxy HTTPS.
- Không expose dashboard ra Internet nếu chưa có TLS/IP allowlist.
- Agent không tự chọn public port; Gateway quyết định port.
- Không log token, SQL password hoặc raw SQL data.
- Chạy Gateway sau Caddy/Nginx để có TLS cho WebSocket nếu dùng Internet public.
- Đặt tên binary rõ ràng: `m-tunnel-gateway`, `m-tunnel-agent.exe`.

## Build commands

```bash
go build -o bin/m-tunnel-gateway ./cmd/gateway
go build -o bin/m-tunnel-agent.exe ./cmd/agent
go build -o bin/m-tunnel-hashpw ./cmd/hashpw
```

Cross compile Windows Agent từ Linux:

```bash
GOOS=windows GOARCH=amd64 go build -o bin/m-tunnel-agent.exe ./cmd/agent
```

Cross compile Linux Gateway từ Windows:

```powershell
$env:GOOS="linux"
$env:GOARCH="amd64"
go build -o bin/m-tunnel-gateway ./cmd/gateway
```

## Echo smoke test

1. Chỉnh `configs/agent.yaml`:

```yaml
agent:
  local_addr: "127.0.0.1:9000"
```

2. Chạy TCP echo server local trên máy Agent.
3. Chạy Gateway và Agent.
4. Từ máy ngoài connect:

```bash
nc VPS_IP 12601
```

Gõ text; nếu echo lại thì tunnel hoạt động.
