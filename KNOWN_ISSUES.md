# KNOWN_ISSUES — 已知问题（如实记录，不隐瞒）

| # | 现象 | 已尝试 | 推测原因 | 状态 |
|---|---|---|---|---|

## KI-3 运行期 Redis 故障的降级语义有限（M4-R2，已知取舍）

- **现象**：`RedisStore` 将一切 miss/传输错误降级为"未找到"并 WARN。运行期 Redis 抖动时 `PlayerByToken` 会 miss → 老玩家 hello 拿到新会话身份，旧对局在内存中仍在但从此不可达；`CreatePlayer` 写失败仍返回 rec（hub 视为已持久，实际重启即丢）。
- **已尝试/缓解**：(1) 每次调用 300ms `opTimeout` 封顶，杜绝网络重试把 hub 冻结（实测降级的三次调用 <3s）；(2) 启动期 ~5s ping 重试 + compose `service_healthy` 依赖消除"启动竞态即永久降级"；(3) 读路径滑动 TTL + 索引双向化，缩小分叉面。
- **推测根因与取舍**：Store 接口是 error-free 契约，hub 运行期内存态才是权威（Redis 仅用于重启恢复/重连持久化）。把"传输错误≠miss"透传进接口需要改签名并让 hub 区分 503——重构成正交鉴权/错误通道超出本期范围。威胁模型：会话 token 明文存 Redis，拿到 Redis 读权限即可接管会话——compose redis 仅内网 expose，按内部可信边界对待（PROD 应加 AUTH/TLS，见 README）。
