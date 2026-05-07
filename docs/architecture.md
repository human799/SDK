# 系统架构说明

## 完整流量链路

```
Android App
    │
    │ TCP  127.23.6.1:9527
    ▼
┌──────────────────────────────────────────┐
│              SDK (sdk.aar)               │
│  本地监听 → 连接池取连接 → obfs编码发送   │
└──────────────────────────────────────────┘
    │
    │ TLS 1.2/1.3（uTLS 随机指纹）
    │ SNI 随机（10个常见域名）
    │ obfs 混淆帧（随机 padding）
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

### 1. 连接池

SDK 维护一个 uTLS 长连接池，App 的每个请求复用池中的连接，而不是每次新建 TLS 握手。

| 参数 | 值 | 说明 |
|------|----|------|
| 最小空闲连接 | 2 | 启动时预热，保持常驻 |
| 最大连接数 | 8 | 超出后不再新建 |
| 连接最大存活 | 10 分钟 | 到期自动淘汰并补充 |
| 空闲超时 | 3 分钟 | 长时间未用的连接主动关闭 |
| 维护周期 | 30 秒 | 后台定期清理过期连接并补充空闲 |

效果：App 发出 100 个请求，服务端看到的是 2~8 条长连接，而不是 100 次 TLS 握手，连接行为与正常用户一致。

连接出错时直接丢弃，不放回池，下次请求会自动补充新连接。

### 2. TLS 伪装层（uTLS）

每条连接建立时，同时随机选取两个参数：

**SNI**（每次随机，从 10 个常见域名中选）：
```
share.note.youdao.com  mail.163.com       www.sohu.com
im.qq.com              www.baidu.com      www.zhihu.com
static.zhihu.com       res.wx.qq.com      open.weixin.qq.com
music.163.com
```

**TLS 指纹**，使用 [uTLS](https://github.com/refraction-networking/utls) 伪造真实客户端 JA3 指纹：

| 预设 | 伪装目标 |
|------|----------|
| `HelloChrome_133` | Chrome 133 |
| `HelloChrome_120` | Chrome 120 |
| `HelloChrome_106_Shuffle` | Chrome 106（扩展随机排序） |
| `HelloFirefox_120` | Firefox 120 |
| `HelloFirefox_105` | Firefox 105 |
| `HelloIOS_14` | iOS 14 Safari |
| `HelloAndroid_11_OkHttp` | Android 11 OkHttp |

SNI 和 TLS 指纹独立随机，组合数 = 10 × 7 = 70 种，每条连接特征不同，JA3 指纹与真实浏览器/手机系统完全一致。

服务端使用自签证书（CN=goedge.cloud），客户端跳过证书校验（InsecureSkipVerify）。支持 TLS 1.2 / 1.3。

TLS 是全双工的，两个方向的数据都在同一条 TLS 会话内加密传输。

### 3. obfs 混淆帧

TLS 内层再加一层自定义帧协议，模糊包长度分布特征。

帧格式：

```
┌──────────┬──────────┬────────────┬─────────────────┬─────────┐
│ magic 4B │  len 4B  │ padLen 1B  │  random padding │ payload │
│0xDEADBEEF│          │  (0~63B)   │   (padLen 字节)  │         │
└──────────┴──────────┴────────────┴─────────────────┴─────────┘
```

- magic：固定 `0xDEADBEEF`，接收端校验，不匹配直接断连
- len：payload 实际长度
- padLen：随机 0~63 字节，每帧不同，使包长度分布随机化
- payload：实际数据

超过 256KB 的 payload 自动拆分为多帧，接收端按帧独立解包，无大小限制。

### 4. PROXY Protocol v1

服务端连接后端时，在 TCP 流开头注入 PPv1 头：

```
PROXY TCP4 <客户端真实IP> <服务器出口IP> <客户端端口> <服务器端口>\r\n
```

后端可通过此头获取 App 的真实出口 IP，无需额外配置。

---

## 文件结构

```
sdk/
  proxy.go     本地监听、连接池调度、relayObfs
  pool.go      连接池、uTLS拨号、随机SNI+随机指纹
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
| TLS 指纹 | 无 | uTLS 随机伪造（7种） |
| SNI 伪装 | 无 | 随机（10个常见域名） |
| 流量混淆 | 无 | obfs 帧 + 随机 padding |
| 连接复用 | 无 | 连接池（2~8条长连接） |
| 适用场景 | 内网调试 | 生产环境 |
