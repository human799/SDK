# 编译文档

## 一、转发服务器 — Windows 编译

### 环境要求

- Go 1.21+（[下载](https://go.dev/dl/)）
- 安装后执行 `go version` 确认

### 编译

编译为 Windows 本地可执行文件：

```cmd
go build -o proxy-server.exe ./server
```

交叉编译为 Linux（在 Windows 上编译，部署到 Linux 服务器）：

```cmd
set GOOS=linux& set GOARCH=amd64& go build -o proxy-server ./server
```

ARM 服务器（如甲骨文 ARM）：

```cmd
set GOOS=linux& set GOARCH=arm64& go build -o proxy-server ./server
```

上传到服务器后加执行权限：

```bash
chmod +x proxy-server
```

### 生成 TLS 自签证书

需要 OpenSSL（推荐用 [Git for Windows](https://git-scm.com/) 自带的 openssl）：

```cmd
openssl req -x509 -newkey rsa:2048 -nodes ^
  -keyout server.key ^
  -out server.crt ^
  -days 3650 ^
  -subj "/CN=goedge.cloud"
```

生成 `server.crt` 和 `server.key`，放在与 `proxy-server.exe` 同目录。

### 启动

```cmd
./proxy-server -listen 0.0.0.0:10443 -target 52.128.229.186:10443 -cert server.crt -key server.key```

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-listen` | 监听地址 | `0.0.0.0:10443` |
| `-target` | 后端源站地址 | `52.128.229.186:10443` |
| `-cert` | TLS 证书路径 | 空（明文模式） |
| `-key` | TLS 私钥路径 | 空（明文模式） |

不传 `-cert` / `-key` 时退化为明文 TCP 转发（调试用）。

### 注册为 Windows 服务（可选）

用 [NSSM](https://nssm.cc/)：

```cmd
nssm install ProxyServer "C:\path\to\proxy-server.exe"
nssm set ProxyServer AppParameters "-listen 0.0.0.0:10443 -target 52.128.229.186:10443 -cert C:\path\to\server.crt -key C:\path\to\server.key"
nssm start ProxyServer
```

---

## 二、Android SDK 编译

### 环境要求

| 工具 | 版本要求 | 说明 |
|------|----------|------|
| Go | 1.21+ | 需在 PATH 中 |
| Android NDK | r25c+ | 通过 Android Studio SDK Manager 安装 |
| gomobile | latest | 见下方安装步骤 |

> NDK 路径示例：`C:\Users\<你>\AppData\Local\Android\Sdk\ndk\25.2.9519653`

### 安装 gomobile

```cmd
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init
```

`gomobile init` 会自动检测 Android NDK，如果找不到需要手动指定：

```cmd
set ANDROID_NDK_HOME=C:\Users\<你>\AppData\Local\Android\Sdk\ndk\25.2.9519653
gomobile init
```

### 编译 AAR

在项目根目录执行：

```cmd
gomobile bind -target=android/arm64,android/amd64 -androidapi 21 -o sdk.aar ./sdk
```

| 参数 | 说明 |
|------|------|
| `-target` | 目标架构，`arm64` 覆盖主流机型，`amd64` 覆盖模拟器 |
| `-androidapi 21` | 最低 Android 5.0，按需调整 |
| `-o sdk.aar` | 输出文件名 |

编译完成后得到 `sdk.aar` 和 `sdk-sources.jar`。

### 集成到 Android 项目

将 `sdk.aar` 复制到 `app/libs/`，然后在 `app/build.gradle` 添加：

```groovy
dependencies {
    implementation fileTree(dir: 'libs', include: ['*.aar'])
}
```

详细接入步骤见 [android-integration.md](android-integration.md)。

---

## 三、流量链路说明

```
Android App
    ↓ TCP (127.23.6.1:9527)
SDK（本地监听）
    ↓ TLS 1.2/1.3（SNI 轮转：youdao / 163 / sohu / qq / baidu）
    ↓ obfs 混淆帧（magic + random padding）
转发服务器（proxy-server.exe）
    ↓ 解 TLS + 解 obfs
    ↓ PROXY Protocol v1（透传真实 IP）
后端源站
```

SDK 侧每条连接随机选取一个 SNI，服务端证书 CN=goedge.cloud（自签），客户端跳过证书校验。
