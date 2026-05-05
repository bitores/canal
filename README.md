# canal

**自托管内网穿透工具** — 将内网服务安全地暴露到公网。

类似 ngrok/cpolar 的开源替代品，使用 Go 语言实现，无需依赖第三方服务。

## 功能特性

- **HTTP 隧道** — 将本地 HTTP 服务暴露到公网
- **TCP 隧道** — 转发任意 TCP 协议（SSH、RDP、数据库等）
- **Token 认证** — 客户端连接时验证身份
- **HTTP Basic Auth** — 为隧道添加访问认证
- **Web Dashboard** — 浏览器实时监控面板
- **断线重连** — 指数退避自动重连
- **TLS 支持** — WebSocket 加密传输

## 快速开始

### 1. 下载预编译二进制

从 [Releases](https://github.com/{{REPO}}/releases) 页面下载对应平台的压缩包，解压后即可使用：

```bash
# 解压
tar xzf canal_<version>_linux_amd64.tar.gz
cd canal_<version>_linux_amd64
```

或以 `.deb` / `.rpm` 包安装：

```bash
# Debian/Ubuntu
sudo dpkg -i canal-server_<version>_linux_amd64.deb
sudo dpkg -i canal-client_<version>_linux_amd64.deb

# RHEL/Fedora
sudo rpm -ivh canal-server_<version>_linux_amd64.rpm
sudo rpm -ivh canal-client_<version>_linux_amd64.rpm
```

### 2. 自行编译

```bash
# 克隆项目
git clone <repo-url> && cd canal

# 编译服务端和客户端
go build -o canal-server ./cmd/server
go build -o canal-client ./cmd/client
```

### 3. 启动服务端

```bash
# 在公网 VPS 上执行
./canal-server --addr :7000 --host your-server.com
```

参数说明：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--addr` | `:7000` | 服务端监听地址 |
| `--host` | `localhost` | 公网主机名或 IP |
| `--tls-cert` | `""` | TLS 证书路径 |
| `--tls-key` | `""` | TLS 私钥路径 |
| `--token-file` | `""` | Token 认证文件路径 |
| `--config` | `""` | 配置文件路径 |

### 4. 启动客户端

```bash
# 在内网机器上执行
./canal-client --server ws://your-server.com:7000 http 3000
```

参数说明：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--server` | `ws://localhost:7000` | 服务端 WebSocket 地址 |
| `--token` | `""` | 认证令牌 |
| `--insecure` | `false` | 跳过 TLS 证书验证 |

位置参数（隧道定义）：

```bash
# HTTP 隧道 — 暴露本地 3000 端口
./canal-client http 3000

# TCP 隧道 — 暴露本地 SSH 服务
./canal-client tcp 22

# 简写（默认 HTTP）— 暴露本地 3000 端口
./canal-client 3000

# 多个隧道
./canal-client http 3000 tcp 22

# 指定完整本地地址
./canal-client http 192.168.1.5:3000
```

### 5. 访问内网服务

启动后服务端会输出隧道公网地址：

```
INFO tunnel active id=web url=http://your-server.com:18080
```

浏览器访问 `http://your-server.com:18080` 即可访问内网的 `localhost:3000` 服务。

### 6. 配置 Token 认证（可选）

防止未授权客户端连接到你的隧道。

```bash
# 1. 生成随机 token
openssl rand -hex 24

# 2. 创建令牌文件
cat > tokens.yaml <<'EOF'
tokens:
  sk_5f8a2b1c...: "my-client"
EOF

# 3. 服务端加载令牌文件启动
./canal-server --addr :7000 --host your-server.com --token-file tokens.yaml

# 4. 客户端携带 token 连接
./canal-client --server ws://your-server.com:7000 --token sk_5f8a2b1c... http 3000
```

如果服务端不配置 `--token-file`，则接受所有连接（开放模式）。

也可以通过 Dashboard 生成 token：

```bash
# 服务端启动后，通过 Dashboard 生成 token（无需手动编辑文件）
curl -X POST http://localhost:8080/api/tokens -H "Content-Type: application/json" \
  -d '{"label":"my-client"}'

# 返回: {"token":"sk_abc123...","label":"my-client"}
```

Dashboard Web 界面也提供 Token 管理功能，访问 `http://your-server.com:8080` 进入 "Token 管理" 面板即可生成和查看 token。令牌文件格式详见"客户端配置"章节。

### 完整示例

```bash
# 终端 1：服务端
./canal-server --addr :7000 --host tunnel.example.com

# 终端 2：启动本地测试服务
python -m http.server 3000

# 终端 3：客户端 - 暴露本地 3000 端口和 SSH 服务
./canal-client --server ws://tunnel.example.com:7000 \
  http 3000 tcp 22

# 终端 4：公网访问
curl http://tunnel.example.com:18080/          # → 本地 3000 端口
ssh user@tunnel.example.com -p 19000           # → 本地 SSH
```

## 配置文件

### 服务端配置

```yaml
# server-config.yaml
listen_addr: ":7000"
public_host: "tunnel.example.com"
tls_cert_file: "/etc/letsencrypt/live/example.com/fullchain.pem"
tls_key_file: "/etc/letsencrypt/live/example.com/privkey.pem"
token_file: "/etc/canal/tokens.yaml"
dashboard_addr: ":8080"
http_port_range: "18080-18180"
tcp_port_range: "19000-19100"
```

### 令牌文件

```yaml
# tokens.yaml
tokens:
  sk_abc123: "client-label-1"
  sk_def456: "client-label-2"
```

### 客户端配置

```yaml
# client-config.yaml
server_addr: "wss://tunnel.example.com:7000"
auth_token: "sk_abc123"
tunnels:
  - id: "web-app"
    type: "http"
    local_addr: "localhost:3000"
    request_host: "myapp"
  - id: "ssh"
    type: "tcp"
    local_addr: "localhost:22"
    remote_port: 2222
  - id: "api"
    type: "http"
    local_addr: "localhost:8080"
    basic_auth:
      username: "admin"
      password: "secret123"
```

## Web Dashboard

服务端启动后，访问 Dashboard 地址（默认 `http://your-server.com:8080`）查看：

- 实时连接状态和统计数据
- 客户端列表
- 隧道列表
- 请求历史记录

## 安全建议

### 生产环境部署

1. **启用 TLS**：使用 Let's Encrypt 为 WebSocket 连接加密

   ```
   ./canal-server --addr :443 --tls-cert fullchain.pem --tls-key privkey.pem
   ```

2. **配置 Token 认证**：防止未授权客户端连接

   ```
   ./canal-server --token-file tokens.yaml
   ```

3. **客户端使用 WSS**：

   ```
   ./canal-client --server wss://tunnel.example.com:443 --token sk_abc123
   ```

4. **限制端口范围**：通过防火墙限制仅有必要端口对外开放

### 注意事项

- Token 文件中的密钥为明文存储，注意文件权限
- TCP 隧道传输经过 Base64 编码会增加约 33% 开销
- Dashboard 无独立认证，建议不对外暴露或通过反向代理增加认证

## 开发

### 依赖安装

```bash
# 国内用户需设置代理
go env -w GOFLAGS=-mod=mod
go env -w GOPROXY=https://goproxy.cn,direct

# 安装依赖
go mod tidy
```

### 运行测试

```bash
# 启动集成测试（HTTP 隧道 + TCP 隧道 + 认证 + Dashboard）
go run ./cmd/integration_test/
```

## 技术栈

- **语言**: Go 1.26+
- **WebSocket**: gorilla/websocket
- **配置**: YAML
- **日志**: slog（标准库）
- **嵌入资源**: Go embed

详细技术实现见 [TECH.md](TECH.md)。
