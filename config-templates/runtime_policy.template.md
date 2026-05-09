# runtime_policy.json 配置说明

## 概述

`runtime_policy.json` 用于配置 SDK 的运行时策略，包括状态机、熔断器、DNS 刷新等行为。所有配置项均可通过下发或本地配置覆盖。

## 配置结构

### 1. 状态机配置 (state_machine)

控制 SDK 在不同网络条件下的状态转换逻辑。

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| NormalToDegradedFail | int | 2 | 从 NORMAL 到 DEGRADED 的连续失败阈值 |
| DegradedToAttackFail | int | 4 | 从 DEGRADED 到 ATTACK 的连续失败阈值 |
| DegradedToNormalSuccess | int | 3 | 从 DEGRADED 到 NORMAL 的连续成功阈值 |
| AttackToRecoverSuccess | int | 5 | 从 ATTACK 到 RECOVERING 的连续成功阈值 |
| RecoverToAttackFail | int | 2 | 从 RECOVERING 到 ATTACK 的连续失败阈值 |
| RecoverToNormalSuccess | int | 5 | 从 RECOVERING 到 NORMAL 的连续成功阈值 |
| EmergencyToRecoverSuccess | int | 2 | 从 EMERGENCY 到 RECOVERING 的连续成功阈值 |
| state_min_dwell_sec | int | 5 | 状态最小驻留时间（秒），防止状态抖动 |

**状态转换流程：**
```
NORMAL --(失败≥2次)--> DEGRADED --(失败≥4次)--> ATTACK --(成功≥5次)--> RECOVERING --(成功≥5次)--> NORMAL
                    --(成功≥3次)--> NORMAL                    --(失败≥2次)--> ATTACK
EMERGENCY --(成功≥2次)--> RECOVERING
```

### 2. 熔断器配置 (circuit_breaker)

控制节点失败后的熔断和退避行为。

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| max_failures | int | 3 | 触发熔断的最大失败次数 |
| base_backoff_ms | int | 1000 | 基础退避时间（毫秒） |
| max_backoff_ms | int | 60000 | 最大退避时间（毫秒） |
| half_open_probe_max_sec | int | 15 | ATTACK/EMERGENCY 状态下半开探测最大间隔（秒） |

**熔断机制：**
- 节点连续失败达到 `max_failures` 次后，进入熔断状态
- 熔断期间使用指数退避策略，间隔从 `base_backoff_ms` 开始，最大不超过 `max_backoff_ms`
- 在 ATTACK/EMERGENCY 状态下，半开探测间隔不超过 `half_open_probe_max_sec`

### 3. DNS 刷新配置

控制域名解析失败后的刷新节奏。

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| dns_refresh_short_sec | int | 25 | DNS 短周期刷新间隔（秒），用于初始故障期 |
| dns_refresh_long_sec | int | 90 | DNS 长周期刷新间隔（秒），用于持久故障期 |
| dns_persist_failures | int | 3 | 进入持久故障期前的失败次数阈值 |

**双层刷新节奏：**
- 前 `dns_persist_failures` 次失败：每 `dns_refresh_short_sec` 秒刷新一次
- 超过阈值后：每 `dns_refresh_long_sec` 秒刷新一次，降低资源消耗

### 4. 网络切换保护 (network_switch_protect_sec)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| network_switch_protect_sec | int | 8 | 网络切换保护窗口（秒），切网后暂缓熔断升级 |

**作用：**
- 网络切换（WiFi ↔ 蜂窝）后，`network_switch_protect_sec` 秒内不升级熔断状态
- 避免切网瞬间的短暂连接失败触发不必要的熔断

### 5. 高防节点心跳 (high_availability_heartbeat_sec)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| high_availability_heartbeat_sec | int | 60 | nodesE 高防节点心跳间隔（秒） |

**作用：**
- 定期探测 nodesE（高防）节点的可用性
- 确保在手动切流后能快速发现恢复的节点

### 6. 最近成功优先 (recent_success_priority)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| recent_success_priority | bool | true | 是否启用"最近成功优先"节点排序 |

**作用：**
- 节点回退时优先尝试最近一次成功的节点
- 减少恢复时间，提高用户体验

## 完整配置示例

```json
{
  "state_machine": {
    "NormalToDegradedFail": 2,
    "DegradedToAttackFail": 4,
    "DegradedToNormalSuccess": 3,
    "AttackToRecoverSuccess": 5,
    "RecoverToAttackFail": 2,
    "RecoverToNormalSuccess": 5,
    "EmergencyToRecoverSuccess": 2,
    "state_min_dwell_sec": 5
  },
  "circuit_breaker": {
    "max_failures": 3,
    "base_backoff_ms": 1000,
    "max_backoff_ms": 60000,
    "half_open_probe_max_sec": 15
  },
  "dns_refresh_short_sec": 25,
  "dns_refresh_long_sec": 90,
  "dns_persist_failures": 3,
  "network_switch_protect_sec": 8,
  "high_availability_heartbeat_sec": 60,
  "recent_success_priority": true
}
```

## 使用方式

### 1. 通过 runtime_policy.json 加载

```go
err := sdkBootstrap.LoadRuntimePolicyFile("path/to/runtime_policy.json")
if err != nil {
    log.Fatal(err)
}
```

### 2. 通过下发配置

SDK 支持从远程下发 `runtime_policy.json`，动态调整运行时行为。

### 3. 默认值

未配置的参数使用默认值，确保向后兼容。

## 监控指标

通过 `Status()` 方法可获取以下诊断信息：

```json
{
  "diagnostic": {
    "state_min_dwell_remaining_sec": 3,
    "network_switch_protect_remaining_sec": 5,
    "dns_refresh_interval_sec": 25,
    "dns_persist_failures_count": 2,
    "recent_success_fallback_count": 12,
    "last_success_node": "1.2.3.4:443"
  }
}
```

## 最佳实践

1. **弱网环境**：适当增大 `state_min_dwell_sec` 和 `NormalToDegradedFail`
2. **高可用要求**：启用 `recent_success_priority`，减小 `half_open_probe_max_sec`
3. **降低功耗**：增大 `dns_refresh_long_sec`，适用于持久故障场景
4. **快速恢复**：减小 `high_availability_heartbeat_sec`，加快节点恢复发现