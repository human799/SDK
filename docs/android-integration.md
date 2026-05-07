# Android SDK 对接文档

## 说明

本 SDK 基于 gomobile 编译为 AAR，集成后 APP 只需按自己原有方式连接本地地址
`127.23.6.1:9527`，SDK 会自动将流量转发到代理服务器 `216.118.241.194:10443`。

```
APP 连接 127.23.6.1:9527
        ↓
    SDK（本地转发）
        ↓
代理服务器 216.118.241.194:10443
        ↓
       源站
```

---

## 接入步骤

### 第一步：添加 AAR

将 `sdk.aar` 复制到 Android 项目的 `app/libs/` 目录。

`app/build.gradle` 添加：

```groovy
dependencies {
    implementation fileTree(dir: 'libs', include: ['*.aar'])
}
```

### 第二步：添加权限

`AndroidManifest.xml`：

```xml
<uses-permission android:name="android.permission.INTERNET" />
```

### 第三步：启动 SDK

在 `Application.onCreate()` 中调用，保持整个 APP 生命周期运行。

**Kotlin：**

```kotlin
import sdk.Sdk

class MyApp : Application() {

    private var proxyClient: sdk.ProxyClient? = null

    override fun onCreate() {
        super.onCreate()
        val client = Sdk.newDefaultProxyClient()
        try {
            client.start()
            proxyClient = client
        } catch (e: Exception) {
            Log.e("Proxy", "start failed: ${e.message}")
        }
    }

    override fun onTerminate() {
        proxyClient?.stop()
        super.onTerminate()
    }
}
```

**Java：**

```java
import sdk.Sdk;
import sdk.ProxyClient;

ProxyClient client = Sdk.newDefaultProxyClient();
try {
    client.start();
} catch (Exception e) {
    Log.e("Proxy", "start failed: " + e.getMessage());
}
```

别忘了在 `AndroidManifest.xml` 注册 Application：

```xml
<application
    android:name=".MyApp"
    ...>
```

### 第四步：发起请求

SDK 启动后，APP 按原有方式直接连 `127.23.6.1:9527` 即可。

**原生 Socket（TCP 长连接）：**

```kotlin
val socket = Socket("127.23.6.1", 9527)
val output = socket.getOutputStream()
val input  = socket.getInputStream()
// 正常读写
```

**OkHttp HTTP 请求：**

```kotlin
val client = OkHttpClient.Builder()
    .proxy(Proxy(Proxy.Type.HTTP, InetSocketAddress("127.23.6.1", 9527)))
    .build()

val request = Request.Builder()
    .url("http://127.23.6.1:9527/your/api")
    .build()

client.newCall(request).enqueue(object : Callback {
    override fun onResponse(call: Call, response: Response) { ... }
    override fun onFailure(call: Call, e: IOException) { ... }
})
```

**Retrofit：**

```kotlin
val okHttpClient = OkHttpClient.Builder()
    .proxy(Proxy(Proxy.Type.HTTP, InetSocketAddress("127.23.6.1", 9527)))
    .build()

val retrofit = Retrofit.Builder()
    .baseUrl("http://127.23.6.1:9527/")
    .client(okHttpClient)
    .addConverterFactory(GsonConverterFactory.create())
    .build()
```

---

## API 说明

| 方法 | 说明 |
|------|------|
| `Sdk.newDefaultProxyClient()` | 创建内置配置的客户端，无需任何参数 |
| `client.start()` | 启动本地监听，失败时抛出异常 |
| `client.stop()` | 停止代理，释放端口 |
| `client.isRunning()` | 返回当前运行状态 |
| `client.localPort()` | 返回实际监听端口（固定为 9527） |

---

## 内置配置

| 项目 | 值 |
|------|----|
| 代理服务器 | `216.118.241.194:10443` |
| 本地监听地址 | `127.23.6.1:9527` |
| 连接超时 | 10 秒 |

---

## 常见问题

**Q：`127.23.6.1` 可以正常使用吗？**
可以。Android 上 `127.x.x.x` 整段都是回环地址，可以正常绑定和连接。

**Q：支持 HTTPS 吗？**
支持。SDK 是纯 TCP 四层透传，不解析应用层协议，TLS 握手在 APP 和源站之间端到端完成，SDK 不感知也不干预。

**Q：APP 进后台代理会断吗？**
建议将 SDK 放在 `Service` 中运行，防止进程被系统回收导致代理中断。

**Q：如何验证代理是否生效？**
查看源站日志，请求来源 IP 应为代理服务器 IP `216.118.241.194`，且 PROXY Protocol 头中携带了 APP 的真实出口 IP。
