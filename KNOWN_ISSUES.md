# KNOWN_ISSUES — 已知问题（如实记录，不隐瞒）

| # | 现象 | 已尝试 | 推测原因 | 状态 |
|---|---|---|---|---|
| K1 | `TestConcurrent100Rooms` 在高负载 CI 下偶发 `send move: write tcp ...: i/o timeout` | 排查 `wsclient` 与 `gorilla/websocket` 源码 | `gorilla/websocket` 默认 `PingHandler` 调 `WriteControl` 设 1s 写超时且不清除，污染后续无 deadline 的 `Send` | ✅ 已修复（`wsclient` 自定义 10s Pong 处理器并每次 `Send` 刷新并清除写超时） |
| K2 | 运行期 Redis 瞬时故障或慢响应影响 `Hub` | (1) 每次调用 300ms `opTimeout` 封顶；(2) `Hub` 新增 `playersByToken` 内存快路径并将全部 `Store` IO 移出 `h.mu` 临界区（`syncRoomStore` 锁外同步） | 原实现将 `Store` IO 放在 `h.mu` 内且 `Hello` 仅查 `Store` | ✅ 运行期会话恢复已由内存快路径兜底且不阻塞 `h.mu`（`TestResumeSurvivesTransientStoreDown`）；跨进程重启仍仅恢复玩家身份与房间元数据，实时棋盘状态驻留内存 `game.Actor` |
