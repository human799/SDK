# 配置管理模块接口骨架

## 对外目标

在不改数据面主链路的前提下，新增控制面能力：

- 读取本地配置与 UUID
- 远程版本检查（HEAD）
- 并发拉取配置（COS/OSS/ZOS）
- 校验/解密/回退
- 固定选点与回退策略输出

## 核心接口（建议）

```go
type Manager interface {
    Init(secret string) error
    PrepareConfig() error
    Current() string
    Probe() string
    Close() error
}
```

## 固定选点规范

每设备固定得到 `A/B/C/D/E` 各 1 个节点，例如 `A3 B1 C2 D1 E1`：

- `idxA = hash(uuid+"|A") % len(nodesA)`
- `idxB = hash(uuid+"|B") % len(nodesB)`
- `idxC = hash(uuid+"|C") % len(nodesC)`
- `idxD = hash(uuid+"|D") % len(nodesD)`
- `idxE = hash(uuid+"|E") % len(nodesE)`

## 回退边界

- 仅在固定 5 节点集合内切换
- 不使用未入选节点
- 固定集合全失败后，进入 `app_domain` 回退

