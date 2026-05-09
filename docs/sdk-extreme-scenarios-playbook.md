# SDK 极端场景评估与改进建议

## 1. 目标

本文用于评估 SDK 在复杂网络与节点波动条件下的行为，并给出改进建议，重点覆盖：

- 弱网、丢包、高抖动
- 存储桶更新延迟/缺失
- 节点大面积不可用
- 域名解析滞后（例如 10 分钟后才切新 IP）
- 多线路动态可用性变化（ABCD/E 分区波动）

---

## 2. 当前能力（基线）

当前 SDK 已具备：

- 固定节点集合策略（每设备 A/B/C/D/E 各 1 个）
- 节点失败后的固定集合内切换
- `app_domain` 回退 + 20~30 秒 DNS 刷新
- 状态机、熔断、指数退避
- 配置缓存 + 条件请求（HEAD/304 + GET）
- 后台自动刷新（默认 10 秒）
- 网络探测（百度 TCP 443）辅助区分"网络故障/节点故障"

---

## 3. 关键场景判断与改进建议

| 场景 | 优先级 | 实施复杂度 | 回滚难度 | 性能影响 | 预估工时 | 改进建议 |
|------|--------|------------|----------|----------|----------|----------|
| **A. 弱网/高丢包/高抖动** | P0 | 中 | 低 | 低 | 2-3天 | <ul><li>增加"弱网容忍窗口"：连续失败阈值按网络质量动态上调</li><li>状态机增加最小驻留时间（建议 5-10 秒），防止 `DEGRADED <-> ATTACK` 抖动</li><li>探测结果引入滑动平均 RTT，不用单次结果判定</li></ul> |
| **B. 切换网络（WiFi ↔ 蜂窝）** | P0 | 低 | 低 | 极低 | 1天 | <ul><li>识别网络切换事件后，短时进入"保护窗口"（建议 8 秒）：暂缓熔断升级，优先快速重建连接</li><li>切网后主动触发一次控制面刷新（而不是等定时器）</li></ul> |
| **C. 三桶中仅一个可用（且可用桶内 IP 不通）** | P1 | 低 | 低 | 低 | 0.5天 | <ul><li>记录"桶成功但节点不可达"指标，便于运维发现错误配置</li><li>增加"桶数据版本健康告警"：连续 5 次拿到坏节点则上报告警事件</li></ul> |
| **D. 域名也不通，且 10 分钟后才解析新 IP** | P1 | 低 | 低 | 低 | 1天 | <ul><li>增加"双层刷新节奏"：短周期（20-30 秒）前 3 分钟，长周期（60-120 秒）进入持久故障期，降低资源消耗</li><li>域名回退失败超过阈值时，状态切 `EMERGENCY` 并输出明确原因</li></ul> |
| **E. 多节点部分可用（如 3 个不通 1 个通）** | P1 | 中 | 低 | 低 | 1-2天 | <ul><li>在候选排序里引入"最近成功优先 + RTT 权重"</li><li>当前节点失败后优先尝试"最近一次成功节点"，减少恢复时间</li></ul> |
| **F. ABC 初始可用，D 不通；随后 ABC 掉线，几分钟后 D 手动恢复** | P2 | 低 | 低 | 低 | 0.5天 | <ul><li>为"手动切流场景"增加半开探测上限：ATTACK/EMERGENCY 状态下缩短半开探测间隔（建议上限不超过 15 秒）</li><li>对 `nodesE`（高防）保留低频心跳（建议 60 秒），防止恢复发现太慢</li></ul> |

---

## 4. 建议补充的策略参数（可配置）

### 4.1 新增配置参数

| 参数名 | 类型 | 默认值 | 说明 | runtime_policy.json 路径 |
|--------|------|--------|------|--------------------------|
| `state_min_dwell_sec` | int | 5 | 状态最小停留时间（秒），防止状态抖动 | `state_machine.state_min_dwell_sec` |
| `dns_refresh_short_sec` | int | 25 | DNS 短周期刷新间隔（秒） | `dns_refresh_short_sec` |
| `dns_refresh_long_sec` | int | 90 | DNS 长周期刷新间隔（秒） | `dns_refresh_long_sec` |
| `dns_persist_failures` | int | 3 | 进入持久故障期前的失败次数阈值 | `dns_persist_failures` |
| `network_switch_protect_sec` | int | 8 | 切网保护窗口（秒） | `network_switch_protect_sec` |
| `half_open_probe_max_sec` | int | 15 | ATTACK/EMERGENCY 状态下半开探测最大间隔（秒） | `circuit_breaker.half_open_probe_max_sec` |
| `high_availability_heartbeat_sec` | int | 60 | nodesE 高防节点心跳间隔（秒） | `high_availability_heartbeat_sec` |
| `recent_success_priority` | bool | true | 是否启用"最近成功优先"排序 | `recent_success_priority` |
| `rtt_weight` | float | 0.3 | RTT 权重（0-1） | `rtt_weight` |

### 4.2 完整配置示例

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
  "recent_success_priority": true,
  "rtt_weight": 0.3
}
```

---

## 5. 监控指标与告警

### 5.1 需要埋点的关键指标

| 指标名 | 类型 | 说明 | 告警阈值 |
|--------|------|------|----------|
| `bucket_success_node_unreachable_count` | Counter | 桶成功但节点不可达次数 | 连续 5 分钟 > 0 |
| `dns_refresh_failures` | Counter | DNS 刷新失败次数 | 1 分钟 > 10 |
| `network_switch_count` | Counter | 网络切换次数 | 1 小时 > 20 |
| `state_transitions` | Counter | 状态转换次数（按状态细分） | DEGRADED→ATTACK 1 分钟 > 3 |
| `node_recovery_time_seconds` | Histogram | 节点恢复耗时（秒） | P95 > 30 |
| `recent_success_fallback_count` | Counter | "最近成功优先"回退次数 | 无 |
| `emergency_state_duration_seconds` | Histogram | EMERGENCY 状态持续时间 | P95 > 60 |

### 5.2 status() 输出增强

建议在 `Status()` 方法输出中增加以下诊断信息：

```json
{
  "state": "ATTACK",
  "last_fallback_reason": "fixed_set_switch",
  "diagnostic": {
    "bucket_success_node_unreachable": 3,
    "dns_failures_last_hour": 8,
    "network_switches_last_hour": 5,
    "state_transitions_last_5min": {
      "DEGRADED->ATTACK": 2,
      "ATTACK->DEGRADED": 1
    },
    "node_recovery_time_p95_seconds": 25,
    "recent_success_fallback_count": 12
  }
}
```

---

## 6. 测试验证方案

### 6.1 场景 A：弱网/高丢包测试

**模拟方法：**
- Windows: 使用 `netsh interface ipv4 set global taskoffload disabled` + `clumsy` 工具模拟高延迟/丢包
- Android/iOS: 使用 Android Emulator Network Profiler 或 Xcode Network Link Conditioner

**测试要点：**
- 100ms RTT + 10% 丢包：状态不应进入 ATTACK
- 500ms RTT + 20% 丢包：状态应进入 DEGRADED 但不应频繁抖动
- 验证滑动平均 RTT 计算正确性

### 6.2 场景 B：网络切换测试

**模拟方法：**
- Windows: 禁用/启用网卡
- Android: WiFi ↔ 蜂窝切换
- iOS: Airplane mode toggle

**验收标准：**
- 切网后 8 秒内恢复连接
- 切网瞬间不触发熔断升级
- 切网后主动刷新控制面

### 6.3 场景 D：DNS 滞后测试

**模拟方法：**
- 修改 hosts 文件模拟 DNS 滞后
- 使用本地 DNS 服务器模拟解析延迟

**测试要点：**
- 前 3 分钟：25 秒刷新一次
- 3 分钟后：90 秒刷新一次
- 域名持续失败 5 次后进入 EMERGENCY 状态

### 6.4 场景 E：节点部分可用测试

**测试要点：**
- 3 个坏节点 + 1 个好节点：应快速找到好节点
- 验证"最近成功优先"排序正确性
- 恢复时间应 < 5 秒

---

## 7. 验收标准

| 场景 | 验收标准 | 测试方法 |
|------|----------|----------|
| 冷启动 | "仅���名可用"场景可成功启动 | 断开所有控制面，仅保留 app_domain |
| 切网 | 10 秒内恢复可用连接（不崩溃、不长卡） | 模拟 WiFi ↔ 蜂窝切换 |
| 节点轮换 | 坏节点不被持续高频重试 | 观察熔断器状态 |
| 存储桶更新 | 10 秒内可检测并应用新配置 | 更新 COS/阿里云配置 |
| 极端故障 | `status()` 可明确给出失败原因与状态 | 触发 EMERGENCY 状态 |

---

## 8. 实施优先级与时间线

### Phase 1：立刻可做（1-2 天）
1. **状态机最小驻留时间**：防止状态抖动（场景 A）
2. **切网保护窗口**：切网后主动刷新（场景 B）
3. **DNS 长故障期降频**：降低功耗（场景 D）

### Phase 2：短期优化（3-5 天）
4. **候选节点排序优化**："最近成功优先 + RTT 权重"（场景 E）
5. **半开探测优化**：缩短 ATTACK/EMERGENCY 探测间隔（场景 F）
6. **监控指标埋点**：关键诊断事件上报

### Phase 3：中期增强（1 周）
7. **滑动平均 RTT**：弱网容忍窗口
8. **桶数据健康告警**：运维可观测性
9. **测试框架完善**：自动化极端场景测试

---

## 9. 向后兼容性说明

| 变更项 | 兼容性 | 说明 |
|--------|--------|------|
| 新增配置参数 | 完全兼容 | 未配置时使用默认值 |
| 状态机最小驻留时间 | 完全兼容 | 0 值表示不启用 |
| DNS 刷新节奏 | 完全兼容 | 未配置时使用默认 25 秒 |
| 半开探测间隔 | 完全兼容 | 未配置时使用默认 60 秒 |
| 节点排序算法 | 完全兼容 | 可通过配置关闭 |

**升级建议：**
- 新版本 SDK 可与旧版本共存
- 配置文件可逐步迁移，未配置项使用默认值
- 建议在测试环境验证后再上线生产

---

## 10. 风险评估

| 改进项 | 技术风险 | 业务风险 | 缓解措施 |
|--------|----------|----------|----------|
| 状态机最小驻留时间 | 低 | 低 | 增加单元测试覆盖 |
| 切网保护窗口 | 极低 | 极低 | 简单时间窗口判断 |
| DNS 长周期刷新 | 低 | 低 | 保留短周期兜底 |
| 节点排序优化 | 中 | 低 | 增加压测验证 |
| 半开探测优化 | 低 | 低 | 限制最大频率上限 |
| 滑动平均 RTT | 中 | 低 | 与现有探测逻辑解耦 |

---

## 附录：相关代码文件

- `sdk/bootstrap.go`：SDK 初始化与状态管理
- `sdk/resilience.go`：状态机与熔断器实现
- `sdk/controlplane.go`：控制面配置获取
- `sdk/nodes.go`：节点选择与排序
- `sdk/policy.go`：运行时策略配置
- `config-templates/runtime_policy.template.json`：配置模板

---

*文档版本：v1.1*  
*最后更新：2026-05-09*
