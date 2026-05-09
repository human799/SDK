# Android SDK 对接文档（最新版）

## 概述

APP 只需要：

1. 集成 `sdk.aar`
2. 调用 `SDKBootstrap`：`init -> prepare -> start`
3. 拿到 `localPort()` 让业务流量连本地回环

支持两种 `secret`：

- 明文 JSON / base64(JSON)（联调）
- RSA token（生产）

## 集成步骤

### 1) 添加 AAR

```groovy
dependencies {
    implementation fileTree(dir: "libs", include: ["*.aar"])
}
```

### 2) 添加权限

```xml
<uses-permission android:name="android.permission.INTERNET" />
```

### 3) Kotlin 示例

```kotlin
val b = Sdk.newSDKBootstrap()
// setDataDir 可选：不传则由 SDK 在 Init 时自动选择应用沙箱内的持久目录
b.init(secret)
b.prepare()
b.setLocalPort(0) // 随机端口
b.start()
val localPort = b.localPort().toInt()
val status = b.status()
```

### 4) Java 示例

```java
SDKBootstrap b = Sdk.newSDKBootstrap();
b.init(secret);
b.prepare();
b.setLocalPort(0);
b.start();
int localPort = (int) b.localPort();
String status = b.status();
```

停止：

```java
b.stop();
```

## 运行建议

- 生产环境建议设置持久化 `UUID`：`setDeviceUUID(...)`
- 建议设置缓存路径：`setCacheFile(...)`
- 如需策略调参：`loadRuntimePolicyFile(...)`
- 可选调用 `setDataDir(...)` 自定义目录；否则 `init` 会按候选目录逐个创建（失败则回退到进程目录下的 `sdk_cache.json`，与旧版一致）

