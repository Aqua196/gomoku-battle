# KNOWN_ISSUES — 已知问题（如实记录，不隐瞒）

| # | 现象 | 已尝试 | 推测原因 | 状态 |
|---|---|---|---|---|
| K1 | `TestConcurrent100Rooms` 在高负载 CI 下偶发 `send move: write tcp ...: i/o timeout` | 排查 `wsclient` 与 `gorilla/websocket` 源码 | `gorilla/websocket` 默认 `PingHandler` 调 `WriteControl` 设 1s 写超时且不清除，污染后续无 deadline 的 `Send` | ✅ 已修复（`wsclient` 自定义 10s Pong 处理器并每次 `Send` 刷新并清除写超时） |
| K2 | 运行期 Redis 瞬时故障或慢响应影响 `Hub` | (1) 每次调用 300ms `opTimeout` 封顶；(2) `Hub` 新增 `playersByToken` 内存快路径并将全部 `Store` IO 移出 `h.mu` 临界区（`syncRoomStore` 锁外同步） | 原实现将 `Store` IO 放在 `h.mu` 内且 `Hello` 仅查 `Store` | ✅ 运行期会话恢复已由内存快路径兜底且不阻塞 `h.mu`（`TestResumeSurvivesTransientStoreDown`）；跨进程重启与节点崩溃后对局由快照恢复（v1.1，见 DESIGN §10 与 K4–K6） |
| K3 | 多实例依赖单个 Redis | — | Directory 的 Lua 脚本在脚本内拼接 `c:bind:*` 键，不满足 Redis Cluster 的“键须作为参数声明”约束；Redis 本身也是单点 | 已知限制：本项目规模单 Redis 足够；要上 Redis Cluster 需把绑定键改为 hash tag 同槽或改写脚本。Redis 宕机期间跨节点功能不可用，同节点对局不受影响 |
| K4 | 崩溃瞬间的最后一手可能丢失 | `TestClusterFailover` / `scripts/cluster-drill.sh` 验证接管 | 快照由异步写入器落库（不阻塞 actor），崩溃若恰好发生在“落子已广播、快照未写完”的亚毫秒窗口，恢复出的棋局少最后一手 | 已知取舍：双方客户端会在恢复后的全量 `game_state` 中看到回退的那一手并可重下；同步写快照会让每手都多一次 Redis 往返 |
| K5 | 接管期间约 `LeaseTTL`（6s）无法落子 | 前端收到 `welcome.roomId` 时把等待窗口放宽到 15s | 崩溃节点的租约需过期才能被接管（优雅退出会主动释放，几乎无等待） | 已知取舍：TTL 越短越容易因 GC/网络抖动误判节点死亡，6s 为折中，可调 |
| K6 | 恢复会重置重连窗口与回合计时 | — | 快照不含各计时器的剩余时间 | 已知取舍：接管后双方各得满额 30s 重连窗口、回合 30s，偏向保护玩家 |

