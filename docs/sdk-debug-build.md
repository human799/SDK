# SDK 调试日志（sdkdebug）

## 编译期开关

使用 Go 构建标签 **`sdkdebug`** 编译 `sdk` 包时，会启用 `[proxysystem-sdk]` 前缀的调试日志（`log` 包输出，Android Logcat 可见）。

不含该标签时，`sdkDebugf` 为空操作，无运行时开销。

### gomobile（AAR）

```bash
gomobile bind -tags sdkdebug -target=android/arm64,android/amd64 -androidapi 21 -o sdk.aar ./sdk
```

本地脚本（嵌入私钥）可加 `-SdkDebug`：

```powershell
.\scripts\build-aar-with-local-key.ps1 -SdkDebug -OutAar sdk-debug.aar
```

### 纯 Go / 测试

```bash
go build -tags sdkdebug ./sdk
go test -tags sdkdebug ./sdk/...
```

## 运行时关闭（仅 sdkdebug 构建）

调试版 AAR 默认打印日志。集成侧可在启动最早处调用：

```kotlin
Sdk.setSDKDebug(false)
```

对应 Java：`Sdk.setSDKDebug(false)`。发布线上若误打了调试包，可用此接口静音。

## 日志内容说明

- **不落密钥**：不打印 `AES-key`、RSA 密文或完整 secret。
- **会打印**：数据目录路径、`app_name` / `sdk_version` / `cos_appid` / `app_domain`、控制面 URL、节点组规模、连接池握手失败、熔断与域名兜底分支、`ERROR` 与 `failLocked` 文案等。
