# 需求文档（商用级 / 生产级 / 高可用版）

## 简介

本系统是一套基于 Go 语言开发的**主控集群（Master Cluster）+ 节点池管理平台**，用于统一管理多个分布式转发节点。主控以集群方式部署，通过 Raft/etcd 进行 Leader 选举，实现多主控高可用；Redis 负责节点在线状态与心跳的高频存储；数据库负责配置数据的持久化。子节点程序运行在各转发服务器上，接受主控指令执行 TCP 端口转发任务，并上报真实转发质量指标。

系统核心用途：构建商用级高可用节点池，通过主控集群统一调度，实现流量的稳定转发、智能调度、自动容灾、灰度发布与多租户隔离。

---

## 词汇表

- **MasterCluster（主控集群）**：由多个 Master 实例组成的高可用集群，通过 Leader 选举保证同一时刻只有一个 Leader 执行调度决策。
- **Master（主控实例）**：运行主控程序的单个服务器节点，参与 Leader 选举，可为 Leader 或 Follower。
- **Leader（主控领导者）**：当前负责执行调度、故障转移、规则下发等核心决策的 Master 实例。
- **Node（子节点/子控）**：运行子节点程序的转发服务器，执行 TCP 端口转发任务，并上报健康指标。
- **Platform（平台）**：第三方业务平台，拥有自己的接口地址和源站 IP，节点为其提供转发服务。
- **Tenant（租户）**：使用本系统的独立业务方，一个主控集群可服务多个租户，租户间权限隔离。
- **Origin（源站）**：平台的后端服务器，分为字节源站和专线源站两类。
- **ForwardRule（转发规则）**：描述节点应将哪个本地端口的流量转发到哪个目标 IP+端口的配置，携带单调递增的 rule_version。
- **RuleVersion（规则版本号）**：每条 ForwardRule 携带的单调递增整数版本号，用于保障配置下发一致性，防止脑裂。
- **ConfirmedVersion（已确认版本号）**：主控为每个节点维护的"该节点已成功 ACK 的最新 rule_version"。
- **Zone（区域）**：节点的逻辑分组，分为 A 区、B 区、C 区、D 区（实时服务区）和备用区（Standby）。
- **ISP（运营商）**：节点所在网络的运营商标识，如 CN2、BGP、联通、电信等。
- **CloudProvider（云厂商）**：节点所在云平台标识，如 AWS、阿里云、腾讯云、本地机房等。
- **NodeLabel（节点标签）**：附加在节点上的键值对标签，用于描述节点能力，如 `bandwidth:100M`、`anti-block:strong`。
- **Heartbeat（心跳）**：节点定期向主控发送的存活信号，包含基础存活信息和质量指标。
- **HealthMetrics（健康指标）**：节点上报的真实转发质量数据，包含成功率、平均延迟、连接失败数。
- **ControlChannel（控制通道）**：主控与节点之间建立的长连接（WebSocket），用于下发指令和接收状态上报。
- **GrayZone（灰度区）**：新节点进入正式服务前的观察区，低流量运行一段时间后方可晋升。
- **CircuitBreaker（熔断器）**：当平台 API 或节点失败率超过阈值时自动断开调用的保护机制。
- **TaskQueue（任务队列）**：基于 Redis 或 Kafka 的异步任务队列，用于解耦平台 API 调用。
- **ConfigVersion（配置版本）**：规则配置的版本号，支持回滚到历史版本。
- **Obfuscation（流量混淆）**：通过 TLS 伪装、HTTP 伪装等手段隐藏节点流量特征的技术。
- **WAL（Write-Ahead Log）**：节点本地的预写日志，每次规则变更先写 WAL 再应用，用于崩溃恢复。
- **NodeSnapshot（节点快照）**：节点定期将当前规则集合写入的本地 JSON 文件，用于加速重启恢复。
- **BlackholeNode（黑洞节点）**：能连接 WebSocket 但实际被运营商丢包、用户体验极差的节点。
- **ProbeRTT（探活 RTT）**：主控主动对节点转发端口发起 TCP 探活所测量的往返时延。
- **ReputationScore（信誉分）**：每个节点维护的综合历史质量评分（0~100），反映节点长期稳定性。
- **PlatformSchedulePolicy（平台调度策略）**：每个平台可独立配置的调度参数，覆盖全局默认策略。
- **NodeUtilization（节点利用率）**：节点当前活跃连接数与最大连接数上限的比值。

---

## 需求

### 需求 1：节点注册与心跳管理

**用户故事：** 作为主控管理员，我希望子节点能自动注册到主控集群并保持心跳，以便主控实时掌握节点的在线状态。

#### 验收标准

1. WHEN 子节点程序启动时，THE Node SHALL 向 MasterCluster 发起注册请求，携带节点 IP、版本号、区域、ISP、CloudProvider 和节点标签信息。
2. WHEN 注册成功后，THE Node SHALL 每 30 秒向 MasterCluster 发送一次心跳包，心跳包中包含基础存活信息和 HealthMetrics（成功率、平均延迟、连接失败数）。
3. WHEN MasterCluster 超过 90 秒未收到某节点的心跳，THE MasterCluster SHALL 将该节点状态在 Redis 中标记为"离线"。
4. WHEN 节点重新上线并发送心跳，THE MasterCluster SHALL 将该节点状态恢复为"在线"。
5. THE ControlChannel SHALL 使用 WebSocket 长连接，以减少频繁建连开销。
6. IF 节点与 MasterCluster 的连接意外断开，THEN THE Node SHALL 按指数退避策略（最长 60 秒）自动重连，重连时优先连接当前 Leader。
7. WHEN 节点心跳中的 HealthMetrics 显示成功率低于 0.90 或平均延迟超过 500ms，THE MasterCluster SHALL 将该节点标记为"质量降级"状态，即使节点仍在线。
8. THE MasterCluster SHALL 将节点在线状态和最新心跳数据存储在 Redis 中，将节点配置数据持久化到数据库，实现读写路径分离。

---

### 需求 2：平台管理

**用户故事：** 作为主控管理员，我希望能在后台配置各第三方平台的接口地址和源站信息，以便节点知道应将流量转发到哪里。

#### 验收标准

1. THE MasterCluster SHALL 提供平台的增删改查接口，每个平台包含：平台名称、接口地址（API URL）、字节源站列表（IP+端口）、专线源站列表（IP+端口）、所属租户 ID。
2. WHEN 管理员新增或修改平台配置时，THE MasterCluster SHALL 将变更持久化到数据库，并生成新的 ConfigVersion 记录。
3. WHEN 平台配置被删除时，THE MasterCluster SHALL 检查是否有节点正在使用该平台，IF 存在关联节点，THEN THE MasterCluster SHALL 拒绝删除并返回关联节点列表。
4. THE MasterCluster SHALL 支持对平台接口地址进行连通性测试，并返回测试结果（HTTP 状态码和响应时间）。
5. WHERE 平台配置了多个源站，THE MasterCluster SHALL 允许管理员为每个源站设置优先级。
6. THE MasterCluster SHALL 支持平台配置的版本回滚，管理员可将平台配置回滚到任意历史 ConfigVersion。

---

### 需求 3：子节点管理

**用户故事：** 作为主控管理员，我希望能在后台添加和配置子节点，并将节点信息同步到对应平台，以便平台感知到可用的转发节点。

#### 验收标准

1. THE MasterCluster SHALL 提供节点的增删改查接口，每个节点包含：服务器 IP、所属平台、绑定源站、转发端口、所属区域（A/B/C/D/备用区/灰度区）、ISP、CloudProvider、节点标签列表、节点权重。
2. WHEN 管理员保存节点配置时，THE MasterCluster SHALL 通过 ControlChannel 向对应节点下发 ForwardRule。
3. WHEN 节点成功应用转发规则后，THE MasterCluster SHALL 通过 TaskQueue 异步调用对应平台的接口地址，将该节点的 IP 和端口注册到平台后台。
4. WHEN 管理员禁用某节点的转发时，THE MasterCluster SHALL 向节点下发"停止转发"指令，并通过 TaskQueue 异步调用平台接口将该节点 IP 从平台后台移除。
5. IF 节点下发规则失败（节点离线或执行报错），THEN THE MasterCluster SHALL 记录失败原因并将节点标记为"配置异常"状态。
6. THE MasterCluster SHALL 支持批量为同一平台的多个节点下发相同的转发规则。
7. WHEN 新节点被添加时，THE MasterCluster SHALL 默认将其放入 GrayZone，不立即分配正式流量。

---

### 需求 4：节点健康监控与自动故障转移

**用户故事：** 作为主控管理员，我希望系统能自动检测节点故障并调度备用节点顶替，以保证服务的持续可用性。

#### 验收标准

1. WHEN MasterCluster 检测到某节点状态变为"离线"，THE MasterCluster SHALL 将该节点标记为"不可用"，并通过 TaskQueue 异步调用对应平台接口将该节点 IP 从平台后台下掉。
2. WHEN 节点被标记为"不可用"后，THE MasterCluster SHALL 从备用区中自动选取一个空闲节点，将其转发规则配置为与故障节点相同。
3. WHEN 备用节点配置完成并成功应用规则后，THE MasterCluster SHALL 通过 TaskQueue 异步调用平台接口将备用节点的 IP 注册到平台后台。
4. IF 备用区没有可用的空闲节点，THEN THE MasterCluster SHALL 向管理员发送告警通知，并记录告警日志。
5. WHEN 故障节点恢复在线后，THE MasterCluster SHALL 通知管理员，由管理员决定是否将其重新纳入服务或保留在备用区。
6. THE MasterCluster SHALL 记录每次故障转移事件的完整日志，包含：故障节点、备用节点、触发时间、平台接口调用结果。
7. WHEN 节点的 HealthMetrics 中转发失败率超过配置阈值（默认 0.20），THE MasterCluster SHALL 自动将该节点摘除并触发故障转移，即使节点心跳仍在线。

---

### 需求 5：TCP 端口转发控制

**用户故事：** 作为主控管理员，我希望能远程控制节点的 TCP 转发规则，包括开启、修改和禁用，以便灵活调整流量路由。

#### 验收标准

1. WHEN MasterCluster 向节点下发 ForwardRule 时，THE Node SHALL 在指定本地端口上启动 TCP 监听，并将流量转发到目标 IP+端口。
2. WHEN MasterCluster 下发"修改转发规则"指令时，THE Node SHALL 停止旧的转发监听，并在新配置上重新启动，整个切换过程不超过 5 秒。
3. WHEN MasterCluster 下发"禁用转发"指令时，THE Node SHALL 关闭对应端口的监听并释放资源。
4. THE Node SHALL 支持同时运行多条转发规则（多端口并发转发）。
5. IF 指定端口已被系统其他进程占用，THEN THE Node SHALL 返回端口冲突错误，THE MasterCluster SHALL 记录该错误并标记节点为"配置异常"。
6. WHEN 节点重启后，THE Node SHALL 自动向 MasterCluster 请求当前应执行的转发规则并重新应用。
7. THE Node SHALL 在转发过程中统计每条规则的成功连接数、失败连接数和平均 RTT，并在心跳中上报。

---

### 需求 6：节点监控面板

**用户故事：** 作为主控管理员，我希望在管理后台看到所有节点和主控集群的实时状态信息，以便快速了解系统整体健康状况。

#### 验收标准

1. THE MasterCluster SHALL 在监控面板中展示每个节点的：在线状态、质量状态、所属区域、ISP、CloudProvider、绑定平台、当前转发规则数量、最后心跳时间、HealthMetrics（成功率、平均延迟）、信誉分（ReputationScore）。
2. THE MasterCluster SHALL 展示主控集群自身的运行信息：当前 Leader 实例、集群成员列表及各成员状态、在线节点总数、离线节点总数、灰度区节点数、备用节点数量、当前告警数量。
3. WHEN 节点状态发生变化（上线/离线/质量降级/配置异常/黑洞），THE MasterCluster SHALL 在监控面板实时更新对应节点的状态显示。
4. THE MasterCluster SHALL 提供节点历史心跳记录查询，支持按时间范围筛选，保留最近 7 天的数据。
5. WHERE 节点处于"不可用"、"质量降级"、"配置异常"或"blackhole"状态，THE MasterCluster SHALL 在监控面板中以醒目颜色标记该节点。
6. THE MasterCluster SHALL 提供按区域、ISP、CloudProvider、标签的节点分组视图。

---

### 需求 7：告警与通知

**用户故事：** 作为主控管理员，我希望在节点故障或备用节点耗尽时收到通知，以便及时介入处理。

#### 验收标准

1. WHEN 节点变为"不可用"状态，THE MasterCluster SHALL 生成一条告警记录，包含：节点 IP、所属平台、触发时间、故障原因。
2. WHEN 备用区节点全部被占用时，THE MasterCluster SHALL 生成"备用节点耗尽"告警。
3. THE MasterCluster SHALL 支持通过 Webhook 方式向外部系统推送告警通知。
4. THE MasterCluster SHALL 提供告警历史查询接口，支持按平台、节点、时间范围筛选。
5. WHEN 告警对应的节点恢复正常后，THE MasterCluster SHALL 自动将该告警标记为"已恢复"。
6. WHEN 节点质量指标持续低于阈值超过 5 分钟，THE MasterCluster SHALL 生成"节点质量持续降级"告警。
7. WHEN 黑洞节点检测触发时，THE MasterCluster SHALL 生成 `blackhole_detected` 类型告警，包含：节点 IP、探活 RTT、节点自报延迟、丢包率、触发时间。

---

### 需求 8：平台接口对接（异步化）

**用户故事：** 作为主控管理员，我希望主控能自动调用第三方平台接口完成节点的上下线操作，且平台接口的慢响应不影响主控核心调度。

#### 验收标准

1. WHEN 需要将节点注册到平台时，THE MasterCluster SHALL 将调用任务投递到 TaskQueue，由异步 Worker 按照平台配置的接口地址发起 HTTP 请求，携带节点 IP 和端口信息。
2. WHEN 需要将节点从平台下线时，THE MasterCluster SHALL 将调用任务投递到 TaskQueue，由异步 Worker 发起 HTTP 请求，携带节点 IP 信息。
3. IF 平台接口调用失败（超时或非 2xx 响应），THEN THE TaskQueue Worker SHALL 按照最多 3 次的策略进行重试，每次间隔 10 秒，超过重试次数后将任务移入死信队列。
4. THE MasterCluster SHALL 记录每次平台接口调用的请求参数、响应内容和耗时。
5. THE MasterCluster SHALL 支持为每个平台配置独立的接口认证信息（如 API Key、Token）。
6. WHEN 某平台接口在 60 秒内连续失败超过 5 次，THE CircuitBreaker SHALL 断开对该平台的调用，并在 30 秒后进行探活重试。
7. THE MasterCluster SHALL 提供死信队列的查询和手动重试接口，供管理员处理积压任务。

---

### 需求 9：系统配置与安全

**用户故事：** 作为系统管理员，我希望主控后台有基本的访问控制，以防止未授权操作。

#### 验收标准

1. THE MasterCluster SHALL 提供管理员账号登录功能，使用用户名+密码认证，密码以 bcrypt 加密存储。
2. WHEN 登录成功后，THE MasterCluster SHALL 签发 JWT Token，有效期为 24 小时。
3. WHILE JWT Token 有效，THE MasterCluster SHALL 允许管理员访问所有管理接口。
4. IF JWT Token 过期或无效，THEN THE MasterCluster SHALL 返回 401 状态码，拒绝请求。
5. THE MasterCluster SHALL 支持通过配置文件（YAML 格式）设置数据库连接、Redis 连接、etcd/Raft 配置、监听端口、JWT 密钥、PSK 等系统参数。
6. THE Node SHALL 使用预共享密钥（Pre-shared Key）与 MasterCluster 进行身份验证，防止未授权节点接入。

---

### 需求 10：多主控高可用（Master Cluster）

**用户故事：** 作为系统管理员，我希望主控以集群方式部署，单个主控实例故障后其他实例能自动接管，节点无感知。

#### 验收标准

1. THE MasterCluster SHALL 支持以多实例方式部署，实例数量不少于 3 个以满足 Raft 多数派要求。
2. WHEN MasterCluster 启动时，THE MasterCluster SHALL 通过 etcd 或内置 Raft 协议进行 Leader 选举，选出唯一 Leader 负责调度决策。
3. WHEN 当前 Leader 实例发生故障或网络分区，THE MasterCluster SHALL 在 10 秒内完成新一轮 Leader 选举，选出新 Leader 继续服务。
4. WHILE Leader 选举进行中，THE MasterCluster SHALL 拒绝写操作并返回 503，读操作可继续服务。
5. THE MasterCluster SHALL 通过 Redis 共享节点在线状态和心跳数据，确保所有 Master 实例看到一致的节点状态视图。
6. WHEN 节点的 ControlChannel 连接断开（因 Leader 切换），THE Node SHALL 自动重连并优先连接新 Leader，重连过程对业务转发无影响。
7. THE MasterCluster SHALL 提供集群健康检查接口，返回各成员状态、当前 Leader 和选举轮次。
8. WHEN 新 Master 实例加入集群，THE MasterCluster SHALL 通过 Raft 日志同步将全量状态复制到新成员，完成后新成员方可参与选举。

---

### 需求 11：智能调度器

**用户故事：** 作为主控管理员，我希望系统能根据节点的综合质量评分智能分配流量，避免流量集中打到单节点。

#### 验收标准

1. THE MasterCluster SHALL 为每个节点计算综合评分，评分公式综合考虑：节点权重、当前连接数、平均 RTT 延迟、转发成功率、带宽使用率、信誉分（ReputationScore）。
2. WHEN 调度器选取节点时，THE MasterCluster SHALL 优先选取综合评分最高的节点，评分相同时随机选取。
3. THE MasterCluster SHALL 支持区域隔离调度：同一平台的流量优先调度到同一 Zone 内的节点，Zone 内无可用节点时跨 Zone 调度。
4. THE MasterCluster SHALL 支持 ISP 和 CloudProvider 维度的隔离调度，管理员可配置流量优先走指定 ISP 或 CloudProvider 的节点。
5. WHEN 单个节点的连接数超过其配置上限的 80%，THE MasterCluster SHALL 将新连接调度到其他节点，防止流量集中。
6. THE MasterCluster SHALL 支持按节点标签进行调度，管理员可为平台配置标签选择器，只有匹配标签的节点才参与该平台的调度。
7. WHEN 调度器无法找到满足所有约束条件的节点，THE MasterCluster SHALL 放宽约束条件（先放宽 ISP/CloudProvider，再放宽 Zone），并记录降级调度日志。
8. WHEN 节点的 ReputationScore 低于 30，THE MasterCluster SHALL 将该节点排除在正式区调度之外，不向其分配新连接。

---

### 需求 12：灰度发布与风控

**用户故事：** 作为主控管理员，我希望新节点先经过灰度观察期再承接正式流量，并在节点质量下降时自动摘除。

#### 验收标准

1. WHEN 新节点被添加到系统时，THE MasterCluster SHALL 将其放入 GrayZone，分配不超过正常流量 10% 的请求进行观察。
2. WHILE 节点处于 GrayZone，THE MasterCluster SHALL 持续监控其 HealthMetrics，观察窗口为 10 至 30 分钟（可配置）。
3. WHEN 节点在灰度观察期内 HealthMetrics 达标（成功率 ≥ 0.95，平均延迟 ≤ 200ms），THE MasterCluster SHALL 将该节点晋升到正式区，分配正常流量。
4. IF 节点在灰度观察期内 HealthMetrics 不达标，THEN THE MasterCluster SHALL 将该节点标记为"灰度失败"，不晋升并生成告警。
5. WHEN 正式区节点的转发失败率在 5 分钟滑动窗口内超过配置阈值（默认 0.20），THE MasterCluster SHALL 自动将该节点摘除并触发故障转移。
6. THE MasterCluster SHALL 提供灰度晋升的手动触发接口，管理员可强制晋升或强制降级节点。
7. THE MasterCluster SHALL 记录每个节点的灰度观察历史，包含观察期内的 HealthMetrics 时序数据。

---

### 需求 13：流量混淆与抗封设计

**用户故事：** 作为节点运营者，我希望节点流量具备混淆能力，降低被识别为代理节点的风险。

#### 验收标准

1. WHERE 节点配置了 TLS 伪装模式，THE Node SHALL 在转发端口上提供合法的 TLS 握手响应，使流量外观与普通 HTTPS 流量一致。
2. WHERE 节点配置了 HTTP 伪装模式，THE Node SHALL 将转发流量封装在 HTTP/WebSocket 协议帧中，使流量外观与普通 HTTP 流量一致。
3. THE Node SHALL 支持随机端口策略：在配置的端口范围内随机选取监听端口，并将实际端口上报给 MasterCluster。
4. THE Node SHALL 支持连接复用（Connection Multiplexing），将多个逻辑连接复用到单个 TCP 连接上，减少连接特征暴露。
5. THE Node SHALL 支持限速配置，管理员可为每个转发规则设置最大带宽（单位 Mbps），超出限速时丢弃超额流量。
6. WHERE 节点启用了流量混淆，THE Node SHALL 在数据包中插入随机填充字节，使数据包长度分布与正常流量相似。
7. IF 节点检测到连接来源 IP 在短时间内发起异常数量的连接（超过配置阈值），THEN THE Node SHALL 对该 IP 实施临时限流。

---

### 需求 14：节点标签系统

**用户故事：** 作为主控管理员，我希望能为节点打标签并按标签调度流量，以便精细化管理节点能力。

#### 验收标准

1. THE MasterCluster SHALL 支持为节点添加、修改和删除标签，标签格式为键值对（如 `bandwidth:100M`、`anti-block:strong`、`provider:aws`）。
2. THE MasterCluster SHALL 支持为平台配置标签选择器，只有节点标签满足选择器条件的节点才参与该平台的调度。
3. WHEN 节点标签发生变化时，THE MasterCluster SHALL 重新评估该节点对所有平台的调度资格，并实时更新调度池。
4. THE MasterCluster SHALL 提供按标签查询节点的接口，支持多标签组合查询（AND 逻辑）。
5. THE MasterCluster SHALL 内置常用标签的枚举值建议，但不限制管理员自定义标签值。

---

### 需求 15：多租户支持

**用户故事：** 作为系统管理员，我希望一套主控集群能服务多个独立平台方（租户），租户间数据和权限完全隔离。

#### 验收标准

1. THE MasterCluster SHALL 支持多租户模式，每个租户拥有独立的平台列表、节点池和配置数据。
2. WHILE 租户 A 的管理员登录时，THE MasterCluster SHALL 只允许其访问租户 A 的资源，不可访问其他租户的数据。
3. THE MasterCluster SHALL 支持超级管理员角色，超级管理员可管理所有租户的资源和配置。
4. WHEN 创建节点时，THE MasterCluster SHALL 要求指定所属租户，节点只能被同租户的平台使用。
5. THE MasterCluster SHALL 为每个租户提供独立的 API 配额限制，防止单租户占用过多系统资源。

---

### 需求 16：限流与熔断

**用户故事：** 作为系统管理员，我希望系统在外部依赖（平台 API）或内部节点出现问题时能自动保护，防止级联故障。

#### 验收标准

1. THE MasterCluster SHALL 为每个平台的 API 调用实现 CircuitBreaker，当失败率超过阈值时自动断开调用。
2. WHEN CircuitBreaker 处于断开状态，THE MasterCluster SHALL 拒绝对该平台的新 API 调用，并返回降级响应。
3. WHEN CircuitBreaker 断开后经过冷却期（默认 30 秒），THE MasterCluster SHALL 允许少量探活请求通过，IF 探活成功 THEN THE CircuitBreaker SHALL 恢复为关闭状态。
4. THE MasterCluster SHALL 为管理 API 实现请求限流，每个租户每分钟的 API 调用次数不超过配置上限。
5. WHEN 节点失败率超过自动摘除阈值，THE MasterCluster SHALL 将节点从调度池中移除，不再向其分配新连接。

---

### 需求 17：配置版本控制

**用户故事：** 作为主控管理员，我希望所有规则配置都有版本记录，并支持回滚，以便在配置错误时快速恢复。

#### 验收标准

1. WHEN 管理员修改平台配置、节点配置或转发规则时，THE MasterCluster SHALL 自动创建新的 ConfigVersion 记录，保存变更前后的完整配置快照。
2. THE MasterCluster SHALL 提供配置版本历史查询接口，返回版本号、变更时间、变更人和变更摘要。
3. WHEN 管理员触发配置回滚时，THE MasterCluster SHALL 将指定资源的配置恢复到目标版本，并向相关节点重新下发规则。
4. IF 回滚操作导致节点规则下发失败，THEN THE MasterCluster SHALL 记录回滚失败日志并生成告警，不自动重试回滚。
5. THE MasterCluster SHALL 保留每个资源最近 50 个版本的历史记录，超出后自动清理最旧的版本。

---

### 需求 18：配置下发一致性保障（防止脑裂）

**用户故事：** 作为系统管理员，我希望主控下发的转发规则能被每个节点可靠确认，即使在 Leader 切换时也不出现脑裂状态，以保证所有节点规则版本一致。

#### 验收标准

1. THE MasterCluster SHALL 为每条 ForwardRule 分配单调递增的 RuleVersion，每次规则变更时 RuleVersion 必须严格递增。
2. WHEN MasterCluster 向节点下发 ForwardRule 时，THE Node SHALL 在成功应用规则后通过 ControlChannel 回复 `rule_ack` 消息，消息中包含已确认的 rule_version。
3. THE MasterCluster SHALL 为每个节点维护 ConfirmedVersion（已确认版本号），记录该节点最后一次 ACK 的 rule_version，并持久化到数据库。
4. WHEN MasterCluster 下发规则后超过 10 秒未收到节点的 `rule_ack`，THE MasterCluster SHALL 重新下发该规则，最多重试 3 次，每次间隔 5 秒。
5. IF 重试 3 次后仍未收到 ACK，THEN THE MasterCluster SHALL 将该节点标记为"配置异常"状态并生成告警。
6. WHEN Node 收到 ForwardRule 时，IF 该规则的 rule_version 不大于节点当前已应用的最大版本号，THEN THE Node SHALL 丢弃该规则并回复 ACK（携带当前已应用版本号），不重复应用。
7. WHEN Leader 切换完成后，THE 新 Leader SHALL 从数据库读取每个节点的 ConfirmedVersion，对所有 ConfirmedVersion 小于最新 RuleVersion 的节点重新下发未确认的规则。

---

### 需求 19：节点侧数据持久化（WAL + 快照）

**用户故事：** 作为节点运营者，我希望节点在主控不可达时仍能使用本地持久化的规则继续转发，并在重启后快速恢复到最新状态，以避免因主控故障导致转发中断。

#### 验收标准

1. WHEN Node 收到规则变更指令时，THE Node SHALL 先将变更记录写入本地 WAL 文件，再将规则应用到转发引擎，确保 WAL 写入先于规则应用。
2. THE Node SHALL 每 60 秒将当前完整规则集合写入本地 NodeSnapshot 文件（JSON 格式），快照文件包含所有当前生效规则及其 rule_version。
3. WHEN Node 重启时，THE Node SHALL 按以下顺序恢复：先从最新 NodeSnapshot 文件加载规则，再回放 WAL 中快照时间戳之后的所有变更记录，最后向 MasterCluster 请求最新规则进行同步。
4. IF Node 重启后 MasterCluster 不可达，THEN THE Node SHALL 使用本地 NodeSnapshot 中的规则继续运行（降级模式），不中断已有转发，并持续尝试重连主控。
5. THE Node SHALL 使用以下 WAL 文件格式：每行一条 JSON 记录，包含操作类型（`apply` 或 `disable`）、rule_version、规则内容和时间戳。
6. WHEN Node 成功与 MasterCluster 同步最新规则后，THE Node SHALL 清理已被快照覆盖的旧 WAL 记录，防止 WAL 文件无限增长。

---

### 需求 20：黑洞节点检测（主控主动探测）

**用户故事：** 作为主控管理员，我希望系统能主动检测被运营商丢包的"黑洞节点"，即使这些节点的 WebSocket 连接仍然正常，以便及时触发故障转移保障用户体验。

#### 验收标准

1. THE MasterCluster SHALL 每 60 秒对每个在线节点的转发端口发起主动 TCP 探活（建立 TCP 连接并发送探测包），记录探活的 ProbeRTT。
2. WHEN 探活完成后，THE MasterCluster SHALL 将 ProbeRTT 与该节点心跳中上报的 avg_latency 进行对比，IF 差值超过 200ms，THEN THE MasterCluster SHALL 生成延迟异常告警。
3. WHEN 某节点连续 3 次探活失败（TCP 连接无法建立或超时），THE MasterCluster SHALL 将该节点的丢包率标记为 100%。
4. WHEN 节点的探活丢包率超过 30%，THE MasterCluster SHALL 生成 `blackhole_detected` 告警，并自动将该节点状态标记为 `blackhole`，触发故障转移流程。
5. WHEN 节点被标记为 `blackhole` 状态后，THE MasterCluster SHALL 停止向该节点分配新连接，并通过 TaskQueue 异步调用平台接口将该节点从平台后台下线。
6. THE MasterCluster SHALL 在监控面板中展示每个节点的最新 ProbeRTT 和探活丢包率，供管理员参考。

---

### 需求 21：成本控制与节点回收机制

**用户故事：** 作为系统管理员，我希望系统能识别长期低利用率的节点并提示回收，以降低云服务器的按量计费成本。

#### 验收标准

1. THE MasterCluster SHALL 每分钟统计每个节点的 NodeUtilization（活跃连接数 / max_conns），并将最近 24 小时的利用率历史保存到数据库。
2. WHEN 某节点连续 6 小时的 NodeUtilization 均低于配置阈值（默认 10%），THE MasterCluster SHALL 生成"低利用率"告警，提示管理员考虑回收该节点。
3. WHEN 管理员手动触发节点回收时，THE MasterCluster SHALL 将该节点的所有转发规则迁移到其他可用节点，迁移完成后自动将节点状态设为"已下线"，并调用平台接口注销该节点。
4. WHERE 管理员已开启"自动回收策略"，THE MasterCluster SHALL 在节点满足低利用率条件时自动执行回收流程，无需人工确认（默认关闭，需管理员显式开启）。
5. THE MasterCluster SHALL 记录每次节点回收的完整日志，包含：回收时间、迁移目标节点列表、平台接口调用结果、触发方式（手动/自动）。
6. THE MasterCluster SHALL 提供节点利用率历史查询接口，支持按节点 ID 和时间范围筛选。

---

### 需求 22：节点信誉系统（Node Reputation Score）

**用户故事：** 作为主控管理员，我希望系统为每个节点维护一个基于历史表现的信誉分，以便调度器优先选择历史稳定的节点，并自动隔离长期表现差的节点。

#### 验收标准

1. THE MasterCluster SHALL 为每个节点维护一个 ReputationScore（信誉分），取值范围为 0 至 100，新节点初始值为 80。
2. THE MasterCluster SHALL 按以下规则更新 ReputationScore：
   - WHEN 节点在某小时内持续在线且成功率 ≥ 0.95，THE MasterCluster SHALL 将该节点的 ReputationScore 加 1（上限 100）。
   - WHEN 节点发生故障转移（被替换），THE MasterCluster SHALL 将该节点的 ReputationScore 减 10（下限 0）。
   - WHEN 节点被检测为黑洞节点，THE MasterCluster SHALL 将该节点的 ReputationScore 减 20（下限 0）。
   - WHEN 节点灰度观察失败，THE MasterCluster SHALL 将该节点的 ReputationScore 减 5（下限 0）。
3. WHEN 节点的 ReputationScore 低于 30，THE MasterCluster SHALL 自动将该节点加入"观察名单"，不参与正式区调度，仅可作为备用节点。
4. THE MasterCluster SHALL 在综合评分公式中加入信誉分权重（W_reputation = 0.15），信誉分越高的节点在调度时获得更高优先级。
5. THE MasterCluster SHALL 提供节点信誉分历史查询接口，记录每次分数变化的原因和时间。

---

### 需求 23：多平台差异化调度策略

**用户故事：** 作为主控管理员，我希望能为不同平台配置独立的调度策略，以满足各平台对节点类型、区域分布和质量要求的差异化需求。

#### 验收标准

1. THE MasterCluster SHALL 支持为每个平台配置独立的 PlatformSchedulePolicy，包含：优先标签选择器、区域权重分配（各区域流量百分比）、最大单节点连接数上限（覆盖全局配置）、是否启用信誉分过滤。
2. WHEN 调度器为某平台选取节点时，THE MasterCluster SHALL 优先使用该平台的 PlatformSchedulePolicy；IF 该平台未配置独立策略，THEN THE MasterCluster SHALL 使用全局默认调度策略。
3. WHERE 平台配置了区域权重分配，THE MasterCluster SHALL 按照配置的权重比例在各区域间分配流量（如 A 区 50%、B 区 30%、C 区 20%）。
4. WHERE 平台配置了优先标签选择器，THE MasterCluster SHALL 优先从匹配标签的节点中选取，无匹配节点时降级到无标签约束的全局调度。
5. WHERE 平台配置了最大单节点连接数上限，THE MasterCluster SHALL 使用该平台级上限替代节点的全局 max_conns 配置，用于该平台的调度判断。
6. WHERE 平台启用了信誉分过滤，THE MasterCluster SHALL 将 ReputationScore 低于 30 的节点排除在该平台的调度之外。
