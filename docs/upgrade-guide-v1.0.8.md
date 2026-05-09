# SDK 升级指南 v1.0.8

## 版本信息

- **版本号**: v1.0.8
- **发布日期**: 2026-05-09
- **升级类型**: 功能增强

## 概述

v1.0.8 版本专注于提升 SDK 在极端网络环境下的稳定性和恢复能力，包括弱网、高丢包、网络切换、节点大规模不可用等场景。

## 新增功能

### 1. 状态机最小驻留时间

**功能说明**: 防止状态在 DEGRADED 和 ATTACK 之间频繁抖动。

**配置参数**: `state_min_dwell_sec` (默认: 5 秒)

**使用方式**:
```json
{
  "state_machine": {
    "state_min_dwell_sec": 5
  }
}
```

**效果**: 
- 状态切换后至少驻留指定时间，避免快速抖动
- 提高状态判断的稳定性

### 2. 网络切换保护

**功能说明**: 网络切换（WiFi ↔ 蜂窝）后暂缓熔断升级。

**配置参数**: `network_switch_protect_sec` (默认: 8 秒)

**使用方式**:
```json
{
  "network_switch_protect_sec": 8
}
```

**API 方法**:
```go
// 手动触发网络切换保护
sdkBootstrap.SetNetworkSwitch()
```

**效果**:
- 切网后 8 秒内不升级熔断状态
- 避免切网瞬间的短暂连接失败触发不必要的熔断
- 切网后自动触发控制面刷新

### 3. 最近成功优先节点排序

**功能说明**: 节点回退时优先尝试最近一次成功的节点。

**配置参数**: `recent_success_priority` (默认: true)

**使用方式**:
```json
{
  "recent_success_priority": true
}
```

**效果**:
- 减少节点恢复时间
- 提高用户体验
- 降低重试次数

### 4. 双层 DNS 刷新节奏

**功能说明**: 根据 DNS 失败持续时间动态调整刷新频率。

**配置参数**:
- `dns_refresh_short_sec` (默认: 25 秒) - 初始故障期刷新间隔
- `dns_refresh_long_sec` (默认: 90 秒) - 持久故障期刷新间隔
- `dns_persist_failures` (默认: 3) - 进入持久故障期的阈值

**使用方式**:
```json
{
  "dns_refresh_short_sec": 25,
  "dns_refresh_long_sec": 90,
  "dns_persist_failures": 3
}
```

**效果**:
- 前 3 次失败：每 25 秒刷新一次
- 超过 3 次失败：每 90 秒刷新一次
- 降低持久故障期的资源消耗

### 5. 半开探测最大间隔

**功能说明**: 限制 ATTACK/EMERGENCY 状态下的半开探测间隔。

**配置参数**: `half_open_probe_max_sec` (默认: 15 秒)

**使用方式**:
```json
{
  "circuit_breaker": {
    "half_open_probe_max_sec": 15
  }
}
```

**效果**:
- 在熔断状态下更快发现恢复的节点
- 缩短恢复时间

### 6. 高防节点心跳

**功能说明**: 定期探测 nodesE（高防）节点的可用性。

**配置参数**: `high_availability_heartbeat_sec` (默认: 60 秒)

**使用方式**:
```json
{
  "high_availability_heartbeat_sec": 60
}
```

**效果**:
- 确保在手动切流后能快速发现恢复的高防节点
- 降低恢复发现延迟

### 7. 增强的 Status 输出

**新增诊断信息**:
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

**字段说明**:
- `state_min_dwell_remaining_sec`: 状态最小驻留剩余时间
- `network_switch_protect_remaining_sec`: 网络切换保护剩余时间
- `dns_refresh_interval_sec`: 当前 DNS 刷新间隔
- `dns_persist_failures_count`: DNS 持久失败次数
- `recent_success_fallback_count`: 最近成功回退次数
- `last_success_node`: 最近成功的节点地址

## 配置文件更新

### 新增配置项

| 参数名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| state_min_dwell_sec | int | 5 | 状态最小驻留时间（秒） |
| network_switch_protect_sec | int | 8 | 网络切换保护窗口（秒） |
| dns_refresh_short_sec | int | 25 | DNS 短周期刷新间隔（秒） |
| dns_refresh_long_sec | int | 90 | DNS 长周期刷新间隔（秒） |
| dns_persist_failures | int | 3 | 进入持久故障期的失败阈值 |
| half_open_probe_max_sec | int | 15 | 半开探测最大间隔（秒） |
| high_availability_heartbeat_sec | int | 60 | 高防节点心跳间隔（秒） |
| recent_success_priority | bool | true | 是否启用最近成功优先 |

### 完整配置示例

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

## 向后兼容性

- **完全兼容**: 未配置的参数使用默认值
- **无需修改**: 现有配置文件继续有效
- **渐进式升级**: 可逐步添加新配置项

## 迁移指南

### 1. 旧版本升级

如果使用旧版本配置文件，直接替换为新版本配置文件即可。未配置的新参数将使用默认值。

### 2. 逐步启用新功能

建议按以下顺序逐步启用新功能：

1. **Phase 1 (立即启用)**:
   - `state_min_dwell_sec`: 5
   - `network_switch_protect_sec`: 8
   - `dns_refresh_long_sec`: 90

2. **Phase 2 (短期启用)**:
   - `recent_success_priority`: true
   - `half_open_probe_max_sec`: 15
   - `high_availability_heartbeat_sec`: 60

3. **Phase 3 (监控后启用)**:
   - 根据监控指标调整参数

### 3. 监控指标

升级后建议关注以下指标：

- 状态抖动次数（应减少）
- 网络切换后的恢复时间（应缩短）
- DNS 刷新频率（应降低）
- 节点恢复时间（应缩短）

## 测试建议

### 1. 单元测试

```bash
go test ./sdk/... -v
```

### 2. 极端场景测试

- 弱网环境测试（100ms RTT + 10% 丢包）
- 网络切换测试（WiFi ↔ 蜂窝）
- DNS 滞后测试（模拟上游 DNS 延迟）
- 节点大规模不可用测试

### 3. 生产环境灰度

1. 先在 10% 用户灰度
2. 观察 24 小时
3. 无异常后扩大到 50%
4. 再观察 24 小时
5. 无异常后全量发布

## 已知问题

无

## 未来改进

- 滑动平均 RTT（弱网容忍）
- 桶数据健康告警
- 自动化极端场景测试

## 技术支持

如有问题，请联系 SDK 团队。