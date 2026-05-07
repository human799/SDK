# 系统架构说明

## 完整流量链路

```
Android App
    │
    │ TCP  127.23.6.1:9527
    ▼
┌─────────────────────────────────┐
│           SDK (sdk.aar)         │
│  本地监听 → TLS拨号 → obfs编码   │
└─────────────────────────────────┘
    │
    │ TLS 1.2/1.3
    │ SNI 轮转（youdao / 163 / sohu / qq / baidu）
    │ obfs 混淆帧
    ▼
┌─────────────────────────────────┐
│        转发服务器 proxy-server   │
│  TLS终止 → obfs解码 → TCP转发   │
└─────────────────────────────────┘
    │
    │ 明文 TCP
    │ PROXY Protocol v1（透传真实客户端 IP）
    ▼
后端源站（Telegram-Fork）
```

---

## 各层说明

### 1. TLS 伪装层

SDK 每条连接建立时，从 SNI 池中轮转取一个域名作为 TLS SNI：

```
share.note.youdao.com
mail.163.com
www.sohu.com
im.qq.com
www.baidu.com
```

抓包看到的 ClientHello 里 SNI 是这些常见域名，流量外观与正常 HTTPS 无异。

服务端使用自签证书（CN=goedge.cloud），客户端跳过证书校验（InsecureSkipVerify）。支持 TLS 1.2 / 1.3。

TLS 是全双工的，SDK→服务端、服务端→SDK 两个方向的数据都在同一条 TLS 会话内加密传输。

### 2. obfs 混淆帧

TLS 内层再加一层自定义帧协议，进一步模糊流量特征。

帧格式：

```
┌──────────┬──────────┬────────────┬─────────────────┬─────────┐
│ magic 4B │  len 4B  │ padLen 1B  │  random padding │ payload │
│0xDEADBEEF│          │  (0~63B)   │   (padLen 字节)  │         │
└──────────┴──────────┴────────────┴─────────────────┴─────────┘
```

- magic：固定 `0xDEADBEEF`，接收端校验，不匹配直接断连
- len：payload 实际长度
- padLen：随机 0~63 字节的随机 padding，每帧不同，使包长度分布随机化
- payload：实际数据

超过 256KB 的 payload 自动拆分为多帧发送，接收端按帧独立解包，无大小限制。

### 3. PROXY Protocol v1

服务端连接后端时，在 TCP 流开头注入 PPv1 头：

```
PROXY TCP4 <客户端真实IP> <服务器出口IP> <客户端端口> <服务器端口>\r\n
```

后端可通过此头获取 App 的真实出口 IP，无需额外配置。

---

## 文件结构

```
sdk/
  proxy.go     本地监听、TLS拨号、SNI轮转、relayObfs
  obfs.go      obfs帧编解码（客户端）
  android.go   gomobile 编译入口

server/
  main.go               启动参数、监听、信号处理
  forwarder/
    forwarder.go        TLS终止、obfs解码、PPv1注入、转发
  obfs/
    obfs.go             obfs帧编解码（服务端，与sdk/obfs.go协议一致）
```

---

## 配置参数

### SDK 默认配置

| 参数 | 值 |
|------|----|
| 转发服务器 | `216.118.241.194:10443` |
| 本地监听 | `127.23.6.1:9527` |
| TLS | 开启 |
| 连接超时 | 10 秒 |

### 转发服务器启动参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-listen` | 监听地址 | `0.0.0.0:10443` |
| `-target` | 后端源站地址 | `52.128.229.186:10443` |
| `-cert` | TLS 证书 PEM | 空（明文模式） |
| `-key` | TLS 私钥 PEM | 空（明文模式） |

不传 `-cert` / `-key` 时退化为明文 TCP 转发（调试用，obfs 也不启用）。

---

## 明文模式 vs TLS+obfs 模式

| | 明文模式 | TLS+obfs 模式 |
|-|----------|---------------|
| 启动参数 | 不带 -cert/-key | 带 -cert/-key |
| SDK 配置 | TLSEnabled=false | TLSEnabled=true（默认） |
| 传输加密 | 无 | TLS 1.2/1.3 |
| 流量混淆 | 无 | obfs 帧 + 随机 padding |
| SNI 伪装 | 无 | 轮转 5 个常见域名 |
| 适用场景 | 内网调试 | 生产环境 |
