# SDK 运行配置与初始化说明

## 最小流程

1. `b := sdk.NewSDKBootstrap()`
2. `b.init(secret)`
3. `b.prepare()`
4. `b.setLocalPort(0)`
5. `b.start()`
6. `port := b.localPort()`

## 可选初始化

- `loadConfigFile(path)`：加载私钥配置（不内置私钥时使用）
- `loadRuntimePolicyFile(path)`：加载状态机/熔断策略
- `setDeviceUUID(uuid)`：设置持久化 UUID
- `setCacheFile(path)`：设置缓存文件路径

## Secret 说明

- 联调：可直接传 JSON 或 base64(JSON)
- 生产：传 RSA token（SDK 使用私钥解密）

