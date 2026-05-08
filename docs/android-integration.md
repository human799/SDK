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
b.setDataDir(filesDir.absolutePath) // 推荐：让 UUID/缓存自动持久化
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
b.setDataDir(getFilesDir().getAbsolutePath()); // 推荐
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
- 推荐先调用 `setDataDir(...)`，SDK 会自动持久化 `deviceUUID` 与缓存文件

