# canal 技术文档

## 架构概述

canal 是一个自托管的内网穿透工具，采用 **客户端-服务端（C/S）** 架构。服务端运行在公网 VPS 上，客户端运行在内网机器上，通过 WebSocket 隧道将公网请求转发到内网服务。

```
公网用户 ←→ 服务端 (:7000 WS + 动态端口) ←→ WebSocket ←→ 客户端 ←→ 内网服务
```

## 项目结构

```
canal/
├── cmd/
│   ├── server/main.go              # 服务端入口
│   ├── client/main.go              # 客户端入口
│   └── integration_test/main.go    # 集成测试
├── pkg/
│   ├── protocol/                   # 通信协议层
│   │   ├── message.go              # 消息类型 & 帧结构
│   │   ├── control.go              # 控制协议消息体
│   │   └── codec.go                # JSON 编解码
│   ├── tunnel/                     # 隧道处理
│   │   ├── types.go                # 公共隧道类型
│   │   └── http_tunnel.go          # 客户端 HTTP 请求处理
│   ├── server/                     # 服务端实现
│   │   ├── server.go               # 核心：WS 监听、注册处理、生命周期
│   │   ├── client_registry.go      # 客户端连接管理
│   │   ├── public_listener.go      # HTTP 公网监听
│   │   ├── tcp_listener.go         # TCP 公网监听
│   │   ├── metrics.go              # 指标收集器
│   │   ├── dashboard.go            # Web Dashboard REST API
│   │   ├── utils.go                # 工具函数
│   │   └── static/index.html       # 前端页面（嵌入二进制）
│   ├── client/
│   │   └── client.go               # 客户端核心：连接、注册、转发
│   ├── auth/                       # 认证模块
│   │   ├── token.go                # Token 认证
│   │   └── basic_auth.go           # HTTP Basic Auth
│   └── config/
│       └── config.go               # 配置结构体
├── go.mod / go.sum
└── TECH.md / README.md
```

## 传输层与协议设计

### 连接模型

每个客户端与服务端之间建立 **单条持久 WebSocket 连接**。所有控制消息和数据消息通过同一条连接传输，通过 `stream_id` 区分不同的请求流。

### 消息格式

所有消息使用 JSON 编码，通过 WebSocket `TextMessage` 传输：

```json
{
  "type": "message_type",
  "stream_id": "uuid-string",
  "tunnel_id": "tunnel-identifier",
  "payload": { /* 类型特定的负载 */ }
}
```

### 消息类型总表

| 类型 | 方向 | 用途 |
|------|------|------|
| `register` | C→S | 客户端注册，携带 token 和隧道定义 |
| `register_ack` | S→C | 注册结果，携带分配的端口和公网 URL |
| `heartbeat` | C→S | 心跳探测（30 秒间隔） |
| `heartbeat_ack` | S→C | 心跳响应 |
| `http_request` | S→C | HTTP 隧道：转发公网请求到客户端 |
| `http_response` | C→S | HTTP 隧道：返回本地服务响应 |
| `tunnel_open` | S→C | TCP 隧道：通知客户端建立本地连接 |
| `tunnel_data` | 双向 | TCP 隧道：传输原始字节流（Base64 编码） |
| `tunnel_close` | S→C | TCP 隧道：通知客户端关闭本地连接 |
| `tunnel_error` | S→C | TCP 隧道：错误通知 |

### 协议交互序列

```
1. 注册阶段
   客户端                                    服务端
     │--- register -------------------------->│
     │    {token, tunnels: [{id, type,        │
     │      local_addr, basic_auth}]}         │
     │                                        │-- 验证 token（可选）
     │                                        │-- 为每个隧道分配端口
     │                                        │-- 启动公网 TCP 监听
     │<-- register_ack -----------------------│
     │    {success, tunnels: [{id,            │
     │      public_url}]}                     │

2. 心跳保活（持续，每 30 秒）
   客户端                                    服务端
     │--- heartbeat ------------------------>│
     │<-- heartbeat_ack ---------------------│

3. HTTP 隧道请求
   公网用户         服务端                    客户端
     │-- HTTP Req -->│                        │
     │               │--- http_request ------>│
     │               │    {method, url,       │
     │               │     headers, body}     │
     │               │                        │-- 转发到本地 HTTP 服务
     │               │                        │-- 读取响应
     │               │<-- http_response -----│
     │               │    {status_code,       │
     │               │     headers, body}     │
     │<- HTTP Resp --│                        │

4. TCP 隧道
   公网用户         服务端                    客户端
     │-- TCP conn -->│                        │
     │               │--- tunnel_open ------->│
     │               │    {local_addr}        │
     │               │                        │-- 连接本地 TCP 服务
     │               │<== tunnel_data =======>│  (Base64)
     │<=============>│   {data: base64}       │<== local TCP ===>
     │               │== tunnel_data ========>│
     │               │   {data: base64}       │
     │  ...          │   ...                  │  ...
     │               │--- tunnel_close ------>│
     │  断开         │                        │-- 关闭本地连接
```

## 核心数据流详解

### HTTP 隧道

**服务端** ([pkg/server/public_listener.go](pkg/server/public_listener.go)):

1. 客户端注册时调用 `CreateHTTPListener()`，从 HTTP 端口范围（默认 18080-18180）分配下一个可用端口，调用 `net.Listen()` 启动 TCP 监听
2. `serveHTTPListener()` 循环接受公网连接，每个连接启动 `handleHTTPConn()` goroutine
3. `handleHTTPConn()` 的处理流程：
   - 将 TCP 连接包装为 `readWriteCloser`（30 秒读写超时，1MB 读取上限）
   - 通过 `http.ReadRequest()` 解析 HTTP 请求
   - 如配置了 Basic Auth，验证 `Authorization` 头
   - 生成 UUID 作为 `stream_id`
   - 将请求封装为 `HTTPRequestPayload`，通过 WebSocket 发送给客户端
   - 创建带缓冲的 channel（容量 1），注册到 `server.pendingResponses[stream_id]`
   - 阻塞等待客户端响应或服务端关闭信号
   - 收到响应后写入原始 TCP 连接，完成请求-响应周期
   - 记录指标（字节数、耗时、状态码）

**客户端** ([pkg/tunnel/http_tunnel.go](pkg/tunnel/http_tunnel.go)):

1. `readLoop()` 收到 `http_request` 消息，调用 `HandleHTTPRequest()`
2. `HandleHTTPRequest()`：
   - 构造完整 URL：`http://<localAddr><path>`
   - 使用标准 `net/http` 创建并发送请求
   - 读取响应体和响应头
   - 封装为 `HTTPResponsePayload` 发回服务端

### TCP 隧道

**服务端** ([pkg/server/tcp_listener.go](pkg/server/tcp_listener.go)):

1. 注册时从 TCP 端口范围（默认 19000-19100）分配端口
2. 公网连接到达后，发送 `tunnel_open` 消息给客户端
3. `readPump()` 从公网 TCP 连接读取数据，通过 `writeTunnelData()` 发送给客户端（Base64 编码，32KB 块大小）
4. 连接断开时发送 `tunnel_close`

**客户端** ([pkg/client/client.go](pkg/client/client.go)):

1. 收到 `tunnel_open` 后，通过 `net.DialTimeout()` 连接本地 TCP 服务（10 秒超时）
2. `localReadPump()`：从本地 TCP 读取数据，Base64 编码后发送 `tunnel_data` 回服务端
3. `handleTunnelData()`：解码 Base64，写入本地 TCP 连接
4. 形成双向数据流：服务端↔客户端之间通过 WebSocket 收发 Base64 数据，各自在另一端读写原始 TCP

**流量控制**：TCP 隧道使用 32KB 固定缓冲区，无应用层流量控制，依赖 WebSocket/TCP 的背压机制。

## 认证机制

### Token 认证 ([pkg/auth/token.go](pkg/auth/token.go))

服务端通过 `--token-file` 加载 YAML 格式的令牌文件：

```yaml
tokens:
  sk_abc123: "client-label-1"
  sk_def456: "client-label-2"
```

- 客户端注册时在 `RegisterPayload.Token` 字段携带令牌
- 服务端在 `handleTunnel()` 中先解析注册消息，再验证令牌
- 验证失败则返回 `register_ack {success: false}` 并关闭 WebSocket 连接
- 未加载令牌文件时，接受所有连接（开放模式）
- `IsEnabled()` 返回 token store 是否启用

### HTTP Basic Auth ([pkg/auth/basic_auth.go](pkg/auth/basic_auth.go))

- 在客户端隧道定义中配置，注册时发给服务端
- 服务端在 `handleHTTPConn()` 中检查公网请求的 `Authorization` 头
- 使用 SHA-256 + `crypto/subtle.ConstantTimeCompare` 实现常时比较，防止时序攻击
- 认证失败返回 HTTP 401，携带 `WWW-Authenticate` 头

## 指标与监控系统

### MetricsCollector ([pkg/server/metrics.go](pkg/server/metrics.go))

使用原子计数器和互斥锁保护的环形缓冲区：

| 计数器 | 类型 | 说明 |
|---------|------|------|
| `RequestsTotal` | `atomic.Int64` | 累计 HTTP 请求数 |
| `BytesSent` | `atomic.Int64` | HTTP 响应字节数（服务端→公网用户） |
| `BytesReceived` | `atomic.Int64` | HTTP 请求字节数（公网用户→服务端） |
| `ActiveStreams` | `atomic.Int64` | 当前活跃 TCP 流数 |
| `TCPBytesSent` | `atomic.Int64` | TCP 数据写入字节数（服务端→公网用户） |
| `TCPBytesRecv` | `atomic.Int64` | TCP 数据读取字节数（公网用户→服务端） |

每个 HTTP 请求记录一条 `RequestRecord`，包含时间戳、方法、路径、状态码、字节数和耗时。历史记录上限 1000 条。

### Dashboard ([pkg/server/dashboard.go](pkg/server/dashboard.go))

- 通过 `//go:embed static/*` 将前端资源嵌入二进制
- REST API 端点：

| 端点 | 返回值 |
|------|--------|
| `GET /api/status` | 运行时间、连接数、隧道数、请求总数、流量、活跃流 |
| `GET /api/clients` | 客户端列表（含隧道详情） |
| `GET /api/clients/{id}` | 单个客户端详情 |
| `GET /api/tunnels` | 所有隧道及公网 URL |
| `GET /api/requests?limit=N` | 最近 N 条请求记录（默认 50） |
| `GET /api/metrics` | 原始计数器值 |

## 依赖库

| 用途 | 库 |
|------|----|
| WebSocket | `github.com/gorilla/websocket` v1.5.3 |
| UUID 生成 | `github.com/google/uuid` v1.6.0 |
| YAML 解析 | `gopkg.in/yaml.v3` v3.0.1 |
| 日志 | `log/slog`（标准库） |
| TLS | `crypto/tls`（标准库） |

## 端口规划

| 用途 | 默认端口 | 范围 |
|------|----------|------|
| WebSocket 控制面 | 7000 | 可配置 |
| Dashboard | 8080 | 可配置 |
| HTTP 隧道 | 18080+ | 18080-18180 |
| TCP 隧道 | 19000+ | 19000-19100 |
