# SDK 节点健康检查设计文档

## 需求目标

1. **检测快**：节点恢复后尽快被发现
2. **恢复快**：可用节点被发现后尽快切换
3. **性能消耗低**：避免过多 goroutine 和 CPU 占用
4. **网络消耗低**：减少不必要的网络请求

## 场景分析

### 场景 1：5 个节点（A/B/C/D/E），只有 B 可用

- A/B/C/D/E 各 1 个节点
- 当前使用 B 节点
- A/C/D/E 都不可用
- A 恢复后，SDK 应该尽快发现并切换

### 场景 2：全部失败 30 分钟后，其中一个节点恢复

- 所有节点和域名都失败 30 分钟
- 进入 EMERGENCY 状态
- 其中一个节点（如 A）恢复
- SDK 应该尽快发现并切换

## 当前实现分析

### 问题

1. **没有定期检测所有节点**
   - `refreshControlPlaneOnce()` 只获取配置，不检测连通性
   - `isEndpointReachable()` 只在 `Prepare()` 中检测主节点

2. **恢复依赖失败触发**
   - 只有当前节点失败时，才会尝试 `candidates`
   - 如果当前节点 B 一直成功，不会检测 A/C/D/E

3. **自动刷新间隔 10 秒**
   - `autoRefreshInterval = 10 * time.Second`
   - 配置更新慢

4. **全部失败后不会主动切换**
   - `refreshControlPlaneOnce()` 获取新配置后，不会主动切换
   - 只有当当前节点失败时，才会尝试新节点

## 设计方案

### 方案：懒加载检测 + 低频定期检测

**核心思路：**
1. **懒加载检测**：失败时立即检测 candidates
2. **低频定期检测**：每 5 分钟检测所有节点
3. **健康状态缓存**：记录每个节点的健康状态
4. **主动切换**：发现更优节点时切换

**实现细节：**

```go
type NodeHealth struct {
    Node       string
    Healthy    bool
    LastCheck  time.Time
    RTT        time.Duration
}

type NodeHealthChecker struct {
    mu       sync.Mutex
    health   map[string]NodeHealth
    interval time.Duration
    timeout  time.Duration
    stop     chan struct{}
    running  bool
}
```

### 检测策略

**懒加载检测（失败时）：**
```go
// 节点失败时，立即检测 candidates
func (b *SDKBootstrap) checkCandidatesOnFailure() {
    healthy := b.concurrentCheck(b.candidates, 1*time.Second)
    b.updateNodeHealth(healthy)
}
```

**低频定期检测：**
```go
// 每 5 分钟检测所有节点
func (b *SDKBootstrap) periodicHealthCheck() {
    allNodes := b.getAllNodes()
    healthy := b.concurrentCheck(allNodes, 3*time.Second)
    b.updateNodeHealth(healthy)
    
    // 检查是否有更优节点
    if best := b.getBestNode(healthy); best != b.currentNode {
        b.switchToNode(best)
    }
}
```

**并发检测：**
```go
// 同时检测所有节点，超时 3 秒
func (b *SDKBootstrap) concurrentCheck(nodes []string, timeout time.Duration) []NodeHealth {
    results := make(chan NodeHealth, len(nodes))
    for _, node := range nodes {
        go func(n string) {
            health := b.checkSingleNode(n, timeout)
            results <- health
        }(node)
    }
    
    var healthy []NodeHealth
    for i := 0; i < len(nodes); i++ {
        select {
        case health := <-results:
            healthy = append(healthy, health)
        case <-time.After(timeout):
            return healthy
        }
    }
    return healthy
}
```

**检测间隔：**
- NORMAL 状态：每 5 分钟检测一次
- DEGRADED 状态：每 2 分钟检测一次
- ATTACK 状态：每 1 分钟检测一次
- EMERGENCY 状态：每 30 秒检测一次

**超时设置：**
- 单节点检测超时：3 秒
- 总检测超时：3 秒（并发情况下）

### 节点排序优化

**排序策略：**
```go
func (b *SDKBootstrap) sortNodesByHealth(nodes []string) []string {
    // 1. 健康节点优先
    // 2. RTT 小的优先
    // 3. 最近成功的优先
    sort.Slice(nodes, func(i, j int) bool {
        hi := b.getHealth(nodes[i])
        hj := b.getHealth(nodes[j])
        
        // 健康节点优先
        if hi.Healthy != hj.Healthy {
            return hi.Healthy
        }
        
        // RTT 小的优先
        if hi.RTT != hj.RTT {
            return hi.RTT < hj.RTT
        }
        
        // 最近成功的优先
        return hi.LastSuccess.After(hj.LastSuccess)
    })
}
```

### 切换策略

**主动切换：**
- 健康检查后检查是否有更优节点
- 如果 `bestNode` 比当前节点更优，切换

**被动切换：**
- 当前节点失败时，按排序尝试 `candidates`

## 预期效果

### 场景 1：只有 B 可用，A 恢复

**时间线：**
```
T=0s    A 恢复
T=5m    定期健康检查发现 A 可用
T=5m    更新节点排序，A 排在 B 前
T=5m    主动切换到 A（如果 A 比 B 更优）
```

**懒加载检测（B 失败时）：**
```
T=0s    B 失败
T=1s    懒加载检测 candidates，发现 A 可用
T=1s    切换到 A
```

### 场景 2：全部失败 30 分钟后，A 恢复

**时间线：**
```
T=0s    所有节点失败，进入 EMERGENCY
T=30m   A 节点恢复
T=30m+5m  定期健康检查（5 分钟间隔）
           发现 A 可用
T=30m+5m  主动切换到 A
T=30m+5m+1s  A 连接成功，恢复
```

**懒加载检测（当前节点失败时）：**
```
T=30m   A 节点恢复
T=30m+X  当前节点失败（X 不确定）
T=30m+X+1-3s  懒加载检测 candidates，发现 A 可用
T=30m+X+1-3s  切换到 A
```

### 恢复时间总结

| 场景 | 恢复时间 | 说明 |
|------|---------|------|
| 懒加载检测 | 1-3 秒 | 节点失败时立即检测 |
| 定期检测 | 5 分钟 | 低频定期检测兜底 |
| EMERGENCY 状态 | 5 分钟 + 当前节点失败时间 | 懒加载兜底 |

## 性能消耗

**并发检测 5 个节点：**
- Goroutine 数量：5 个
- 内存开销：约 10KB
- CPU 开销：极小（主要是网络 I/O 等待）

**网络消耗：**
- 每次检测：5 个 TCP 连接
- 每次检测耗时：~3 秒
- 每小时检测次数：12 次（5 分钟间隔）
- 每小时连接数：60 个

## 实现计划

### Phase 1：节点健康检查器 ✅
1. 实现 `NodeHealth` 结构体
2. 并发检测所有节点
3. 记录健康状态

### Phase 2：懒加载检测 ✅
1. 节点失败时检测 candidates
2. 更新健康状态
3. 重新排序

### Phase 3：低频定期检测 ✅
1. 每 5 分钟检测所有节点
2. 主动切换到更优节点

### Phase 4：配置参数 ✅
1. 添加配置参数
2. 支持 runtime_policy.json
3. 支持动态调整

## 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| health_check_interval_sec | 300 | 健康检查间隔（秒） |
| health_check_timeout_ms | 3000 | 单节点检测超时（毫秒） |

**说明：**
- 使用统一的健康检查间隔，无需根据状态调整
- 懒加载检测在失败时立即触发
- 主动切换在健康检查后自动进行

## 测试验证

### 测试场景

1. **单节点恢复**
   - 4 个节点死亡，1 个恢复
   - 验证恢复时间 < 5 分钟（定期检测）
   - 验证懒加载恢复时间 < 1 秒（失败时）

2. **全部失败后恢复**
   - 所有节点失败 30 分钟
   - 1 个节点恢复
   - 验证 EMERGENCY 状态恢复时间 < 30 秒

3. **多节点恢复**
   - 2 个节点恢复
   - 验证选择最优节点

4. **网络抖动**
   - 节点频繁恢复/死亡
   - 验证稳定性

5. **性能测试**
   - 检测 100 个节点
   - 验证资源消耗

## 总结

**目标：**
- 检测快：节点恢复后 30 秒 - 5 分钟内发现
- 恢复快：发现后立即切换
- 性能低：并发检测，资源消耗低
- 网络低：低频定期 + 按需懒加载

**关键改进：**
1. 懒加载检测（失败时立即检测）
2. 低频定期检测（每 30 秒 - 5 分钟）
3. 基于健康状态排序
4. 主动切换到更优节点
5. 动态检测间隔（根据状态调整）

**恢复时间：**
- **最优情况**：1 秒（懒加载检测）
- **最差情况**：5 分钟（定期检测兜底）
- **EMERGENCY 状态**：30 秒（高频检测）