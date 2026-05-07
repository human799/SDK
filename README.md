# TCP四层转发代理系统

## 架构

```
Android App
    ↓ SOCKS5 (127.0.0.1:本地端口)
Client SDK (Go/gomobile)
    ↓ TCP + SOCKS5
转发服务器 (server/main.go)
    ↓ TCP
目标服务器
```

## 服务端部署

```bash
go build -o proxy-server ./server
./proxy-server -listen :8888
```

## Android SDK 编译

需要安装 gomobile：

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
gomobile bind -target=android -o sdk.aar ./sdk
```

生成 `sdk.aar` 后放入 Android 项目的 `libs/` 目录。

## Android 使用示例 (Kotlin)

```kotlin
import proxysystem.sdk.Sdk
import proxysystem.sdk.ProxyConfig

val config = Sdk.newProxyConfig("your-server-ip", 8888)
val client = Sdk.newProxyClient(config)

// 启动本地代理
client.start()
val localPort = client.localPort().toInt()

// 方式1：设置JVM全局SOCKS5代理（影响所有Java网络请求）
System.setProperty("socksProxyHost", "127.0.0.1")
System.setProperty("socksProxyPort", localPort.toString())

// 方式2：OkHttp单独配置
val proxy = Proxy(Proxy.Type.SOCKS, InetSocketAddress("127.0.0.1", localPort))
val okHttpClient = OkHttpClient.Builder().proxy(proxy).build()

// 停止
client.stop()
```

## 注意事项

- Android 需要 `INTERNET` 权限
- 如需代理所有APP流量（VPN模式），需结合 Android VpnService API，
  将 tun 设备流量转换为 SOCKS5 请求发给本 SDK
- 服务端建议配合 TLS 或 SSH 隧道加密传输
