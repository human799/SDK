# 跨平台打包说明（Android + iOS）

## 目录

- Android（Windows/Git Bash）：`scripts/build-android-aar.sh`
- Android（PowerShell）：`scripts/build-aar-with-local-key.ps1`
- iOS（macOS）：`scripts/build-ios-xcframework.sh`
- iOS（macOS，内置私钥）：`scripts/build-ios-xcframework-with-local-key.sh`

---

## Android（Windows + Git Bash）

```bash
cd /d/GitHub2/SDK
./scripts/build-android-aar.sh
```

默认行为：

- 从 `config-templates/private_key.pem` 读取私钥
- 构建 `sdk.aar`
- 私钥以 `-ldflags -X proxy-system/sdk.EmbeddedPrivateKeyB64=...` 注入

可传 PowerShell 脚本参数，例如：

```bash
./scripts/build-android-aar.sh -OutAar sdk-test.aar -Target "android/arm64,android/amd64" -AndroidApi 21
```

---

## iOS（macOS）

```bash
cd /path/to/SDK
chmod +x ./scripts/build-ios-xcframework.sh
./scripts/build-ios-xcframework.sh
```

默认输出：`sdk.xcframework`

自定义输出名：

```bash
./scripts/build-ios-xcframework.sh sdk-ios.xcframework
```

### iOS（macOS，内置本地私钥）

```bash
chmod +x ./scripts/build-ios-xcframework-with-local-key.sh
./scripts/build-ios-xcframework-with-local-key.sh
```

可选参数：

```bash
./scripts/build-ios-xcframework-with-local-key.sh ./config-templates/private_key.pem sdk-ios.xcframework
```

---

## 环境要求

- Go 1.21+
- `gomobile` / `gobind`

安装：

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init
```

---

## 重要说明

- iOS 构建必须在 macOS 执行（需要 Apple 工具链）
- Android 可在 Windows 执行

