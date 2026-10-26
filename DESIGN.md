# DESIGN — 实时在线五子棋对战平台

> 架构文档。随里程碑更新；协议细节见 PROTOCOL.md（冻结契约）。

## 1. 总体架构

```
                         ┌───────────── Caddy / 任意负载均衡（无粘性会话）────────────┐
                         ▼                                                          ▼
┌────────────┐  WS   ┌──────────────────────────────┐   ┌──────────────────────────────┐
│ React SPA  │◄────► │ node A (Go)                  │   │ node B (Go)                  │
│ lobby/game │       │  conn pumps ×2 / socket      │   │  conn pumps ×2 / socket      │
│ replay     │       │  Hub ── 连接侧：会话、大厅请求 │   │  Hub ── 连接侧                │
└────────────┘       │       └ 房主侧：本节点的对局  │   │       └ 房主侧                │
                     │  Game actor ×N（每局一个      │   │  Game actor ×M               │
                     │  goroutine + mailbox，AI 驱动 │   │                              │
                     │  也走同一 mailbox）           │   │                              │
                     └──────┬──────────────┬────────┘   └──────┬──────────────┬────────┘
                            │ cluster.Bus  │ Directory          │              │
                            ▼ (pub/sub)    ▼ / Store            ▼              ▼
                     ┌──────────────────────────────────────────────────────────────┐
                     │ Redis：会话与棋谱（store）· 玩家位置/房间绑定/房间表/匹配队列/  │
                     │ 对局快照/租约/心跳（cluster.Directory）· 节点间消息（pub/sub）  │
                     └──────────────────────────────────────────────────────────────┘
   无 Redis 时：单进程，cluster.MemDirectory + MemNetwork，代码路径完全相同。
```

## 2. 模块划分

| 模块 | 职责 |
|---|---|
| `internal/protocol` | 消息类型、常量（超时/窗口，可注入测试值）、错误码 |
| `internal/game` | 五子棋规则纯逻辑：落子校验、四方向胜负、平局；Game actor：回合计时、断线窗口、事件邮箱 |
| `internal/hub` | 一个节点：连接侧（会话、hello/恢复、大厅请求、转发对局指令）与房主侧（本节点拥有的对局、观战、快照、租约与接管），大厅广播 |
| `internal/cluster` | 多节点共享状态 `Directory`（玩家位置、房间绑定、房间表、匹配队列、对局快照、租约、心跳）与节点间消息 `Bus`；内存版与 Redis 版各一，同一套契约测试 |
| `internal/ws`（或 server） | WS 升级、readPump/writePump、心跳、hello 绑定 |
| `internal/store` | `Store` 接口：玩家会话（token 恢复）与已结束对局的棋谱；`MemStore` / `RedisStore` |
| `internal/ai` | v2 引擎：增量评估、置换表、VCF/VCT、迭代加深 PVS（§7）；`baseline/` 冻结的 v1 引擎 |
| `cmd/server` | 组装、HTTP 路由、静态资源托管 |
| `cmd/wsclient` | 脚本化 WS 客户端（集成测试与 M3 压测共用） |
| `cmd/loadtest`（M3） | 并发压测：连接耗时、广播延迟 P50/95/99、资源占用 |
| `cmd/aiarena` | AI 对弈：v2 对 v1 或 v2 各难度互搏，交换先后，输出胜率与搜索统计 |

## 3. 并发模型（选型已定）

- **每连接 2 goroutine**（readPump / writePump），下行经带缓冲 channel（SEND_BUF=64，满则判慢消费断开）。
- **每局 1 actor goroutine**：落子/认输/断线/重连/tick 全部串行进 mailbox → 对局状态无锁、无竞态。计时器用 `time.Timer` 在 actor 内 select。
- **Hub 用一把互斥锁保护本节点的表**（会话、本节点拥有的对局），持锁期间**不做任何 Directory/Bus/Store 调用、不写网络、不向 actor 投递**；准入的原子性交给 Directory（`BindPlayer` / `ClaimSeat` / `Enqueue` 在集群范围原子），会话字段只缓存结果用于路由。因此与 actor、网络、Redis 之间没有锁序问题。
- 备选方案：全局单事件循环（更简单但单点吞吐受限）、每玩家 actor（房间状态要拆两处）。选 mutex-Hub + per-game actor：对局热路径无锁，跨房间操作极少。

## 4. 状态机

### 玩家

`connecting →(hello)→ lobby →(create/join/matchmake/play_ai)→ in_room_waiting → playing →(game_over)→ lobby`；任何状态断线 → `disconnected`（对局中启动 30s 判负计时；大厅中直接清出队列/座位）。

### 房间

`waiting →(满座)→ playing →(game_over)→ finished→删除`。waiting 房主离开 → 房间删除。等待中的房间只存在于 Directory，**谁处理加入（或匹配成对、或 play_ai），谁的节点就启动并拥有这局**；对局中的房间由房主节点的 actor 持有，房间记录随座位在线状态同步到 Directory。

### 对局

`playing{turn, deadlines} → finished{winner, reason}`。

## 5. 关键取舍（详见 DECISIONS.md）

- 全量 GameState 广播而非增量（225 cell ≤1KB，简单幂等，重连与正常流同构）。
- 服务端权威：客户端只做展示与输入，杜绝信任边界问题。
- token 会话恢复：重连实现只依赖内存会话表，天然可测；M4 换 Redis 即获得跨重启恢复。
- AI 走同一 actor mailbox（M2）：AI = 一个内嵌“假客户端”，不引入第二条协议路径。

## 6. M1 评审后加固（docs/reviews/m1.md → 修复）

- **会话生命周期**：hello{token} 命中在线会话 = takeover（关旧绑新）；断线会话 TTL 淘汰（非对局短 TTL + 定期扫描，对局中会话豁免）；服务端重启/token 失效时客户端经 welcome 清理自身视图。
- **广播背压**：room_list 在连接层脏位合并，只投递最新一份，防止 churn 踢出正常客户端。
- **actor 韧性**：run() defer recover + 无条件 onOver 回收房间与玩家；timer 命中先排空 mailbox 再裁决（先到先生效）；双方同断时判负挂起，重连方到场再结算。
- **时钟同步**：welcome/game_state 携带 serverNow，前端用 EMA 估算 offset 渲染倒计时。

## 7. AI 设计（internal/ai，v2 引擎）

- **结构**：`driver.go`（嵌入式假客户端）不变；引擎拆为 `lines.go`（72 条可成五的线及每格所在线的查表）、`position.go`（增量棋盘与评估）、`tt.go`（置换表）、`vcf.go` / `vct.go`（威胁求解）、`search.go`（迭代加深 PVS）、`engine.go`（难度预设与决策顺序）。`baseline/` 冻结 v1 引擎，仅供 `cmd/aiarena` 与基准测试做对照。
- **与对局的接口**：Driver 通过 actor 的 `SetObserver` 钩子收 GameState，轮到 AI 才思考；结果经 `Actor.Move` 回投，**与真人落子走同一条 mailbox 路径**。全局信号量把同时思考的 AI 数限制在 CPU 核数（搜索是 CPU 密集，超额并行只会让每局都搜得更浅）；思考截止取「预设时长」与「回合截止 −1.5s」的较小者，排队再久也不会超时判负。
- **增量评估**：落子只重算经过该格的 4 条线（单次扫描同时算黑白，滑动窗口计数）。每条线缓存：棋型分（5 格窗口按子数计分，另加活三 `.XXX.` / 跳活三 `.X.XX.` 与「一线两个成五点」活四加成）、成五点掩码、成四点掩码、成三点掩码。评估 = 己方总分×1.1 − 对方总分（行棋方多一个先手）。实测增量更新 0.92µs，整盘重建 10.0µs（`docs/evidence/ai-bench.txt`）。
- **搜索**：迭代加深 + PVS（主变例全窗口、其余零窗口试探）；Zobrist 哈希置换表（mate 分按层数归一化存取）存界与最佳着，下一层迭代和下一手都复用；置换表最佳着与 killer 着提前；候选点为距已有棋子 2 格内的空点，按双方 `pointScores` 一次遍历打分后只做 top-k 部分排序。精确战术在每个节点先于置换表判定：能成五即胜；对方两个成五点即负；对方单个成五点时只搜封堵且**不消耗深度**（强制应手延伸，冲四链能读到底）。用时过 45% 预算即不再开启下一层迭代。
- **VCF（连续冲四）**：进攻方只下成四的棋，防守方应手唯一；一手造出两个成五点即胜。失败局面按 Zobrist 记忆。随机局面测试中找到最长 19 手的连续冲四杀，且每个报告的杀都在强制应对下逐手走通（`TestVCFSoundOnRandomPositions`）。
- **VCT（冲四 + 活三）**：活三的防守点取「进攻方所有成四点 + 防守方所有冲四」的超集——只会漏掉杀，不会虚报杀；对每个防守都要有后续 VCT 才算成立（AND-OR 搜索，节点预算封顶）。`TestVCTHoldsAgainstSearch` 让困难档 AI 当防守方，12 个 VCT 局面全部被走通。
- **决策顺序**（`choose`）：成五 → 堵五 → 己方 VCF → 己方 VCT → （easy/normal）造活四 / 堵活四 → 搜索；搜索前若对方有 VCF/VCT，先剔除走后对方仍能杀的候选（先保证破 VCF，再尽量破 VCT；全部破不了则保留全集让搜索选最顽强的一手）。
- **难度**：easy = 2 层、广度 6、根节点 ±40% 噪声、无威胁求解（刻意可赢）；normal = 4 层 + 6 步内 VCF；hard = 迭代加深到 1.8s + VCF(12)/VCT(4) 攻防 + 64K 置换表。
- **强度（300ms/手，每个开局交换先后各一局，共 40 局）**：hard 对 v1 hard 38 胜 2 负（95%），normal 对 v1 normal 70%，easy 对 v1 easy 45%（持平，设计如此）；新引擎内部 hard 对 normal 78%、normal 对 easy 90%。原始输出见 `docs/evidence/ai-arena-*.txt`，复现：`go run ./cmd/aiarena -openings 20 -think 300ms -a hard -b v1:hard`。
- **生命周期**：actor 结束关 `done` → driver.loop 退出（排队等信号量时同样退出）；AI 座位 `connected` 恒 true，断开/认输只清理真人侧。

## 8. M4 工程化（部署/持久化/观测）

- **交付形态**：单一 Dockerfile 多阶段（node 构建 dist → go 构建 server → debian-slim 非 root 运行）；compose 一条命令起 app+redis。前端仍由后端 :8080 同端口托管（无跨域、无第二服务）。
- **Store**：`internal/store` 接口双实现——MemStore（默认，按值拷贝隔离指针）与 RedisStore（`REDIS_ADDR` 启用；sessions 24h TTL + token 二级索引，棋谱 30 天；错误一律降级 miss+warn，绝不阻塞对局热路径）。两实现共用一套表驱动 contract 测试，RedisStore 走 miniredis（进程内真协议）。房间表等实时共享状态在 `internal/cluster`（§10）。
- **观测**：slog 结构化事件（connect/resume/disconnect/room/game over/AI 局，带 playerId/roomId）；`/metrics` JSON：connections/lobbyPlayers/rooms/activeGames/gamesPlayed/totalMoves/movesPerSec(60s)/aiGames（actor SetMoveHook 计数）。
- **CI**：`.github/workflows/ci.yml`——backend vet + `go test -race`（AI 性质测试另跑一遍非 race）、golangci-lint（gosec/errorlint/bodyclose 等），frontend typecheck/lint/vitest/build，`docker-gate` 单实例容器关卡，`cluster-gate` 双实例 + Caddy 关卡（经负载均衡全流程、优雅重启演练、`kill app2` 崩溃接管演练）。
- **关卡**：`WS_ADDR=ws://host:port/ws go test -run Container ./internal/integration` 对运行中实例（容器）跑 match/整局/重连/AI 全流程。

## 9. 安全与威胁模型（M4 评审补充与架构加固）

- 会话 token（43 字符随机）即身份凭据，明文存于 Redis `pt:`/`p:` 键与内存；部署边界内视为可信——compose 的 redis 仅 `expose` 内网不对外发布。对公网部署应追加 Redis AUTH/TLS（或改回纯内存）。
- **锁外 IO 与内存快路径**：所有 `Store` / `Directory` 读写都在 `h.mu` 之外（`Hello` 先查本节点 `playersByToken` 快路径，未命中才查 `Store`），Redis 抖动不会冻结 Hub；同节点的对局（玩家与房主在同一节点）路由全在本地，运行期 Redis 瞬时故障不丢活跃对局（`TestResumeSurvivesTransientStoreDown` 验证）。
- **滥用防护**：每连接上行令牌桶（20/s，突发 40）、单 IP 并发连接上限（默认 32，`TRUST_PROXY=1` 时按 `X-Forwarded-For`）、`http.Server` 读头超时防慢速连接、静态托管走 `os.DirFS`（路径无法逃逸 dist）。

## 10. 多实例集群与故障接管（internal/cluster + hub）

**目标**：任意多个实例挂在一个不带粘性会话的负载均衡后面，表现得像一台服务器；任何一个实例崩溃或重启，进行中的对局都不丢。单实例重启只是它的特例。

- **房主（owner）模型**：每局棋只在一个节点上跑（它的 `game.Actor`），这个节点是房主；玩家可以连在任何节点。玩家所在节点（连接侧）把 `move`/`resign`/断线/重连/观战等指令转给房主；房主经 `deliver` 把帧送回玩家所在节点。双方同在房主节点时全程本地，不经 Redis。
- **Directory（共享状态）**：玩家位置（节点 + 递增 epoch）、玩家↔房间绑定（准入控制，一人至多一房）、房间表（大厅渲染直接读它）、匹配队列、对局快照、对局租约、节点心跳。Redis 版里单键用普通命令，比较再写用短 Lua，匹配队列是**一个 Lua 脚本原子完成“清理过期项 + 入队 + 配对 + 绑定”**（最初用 WATCH 乐观事务，在 20 路并发下重试耗尽丢人，被契约测试抓到后改掉）；`ClaimSeat`/`CloseWaiting` 只涉及一个房间、冲突少，用 WATCH。
- **Bus（节点间消息）**：每节点一个 pub/sub 频道加一个广播频道。出站经单一队列 + 单一发布 goroutine：同一发送方的消息按序到达（“seated” 一定先于 `game_start`），actor 永不因网络等待被阻塞；积压超过 50ms 才丢弃并告警（丢失的帧由下一次全量 `game_state` 自愈）。
- **epoch 防护**：每次 hello 都在 Directory 里给该玩家的 socket 发一个更大的 epoch。玩家换节点时旧节点被 `kick`（静默丢弃会话，不替玩家结算任何事）；迟到的旧 socket 断线通知若 epoch 小于房主已见的最新 epoch，直接忽略——“刚在 B 重连、A 的旧连接才报断线”不会把在线玩家判为离线。
- **快照与租约**：房主在每次状态变化后把对局快照（座位 + 全部着法 + AI 难度）交给异步写入器（每局只保留最新一份，单 goroutine 落库，不阻塞 actor；删除与写入串行，已结束的对局不会被迟到的写入“复活”）。房主每 `RecoverInterval`（1s）续租，租约 `LeaseTTL`（6s）。
- **接管**：每个节点每秒扫描快照，发现租约已失效的对局就抢租约、二次确认快照仍在，然后 `game.Restore` 按着法重建棋盘（人类座位一律离线、各给满 30s 重连窗口、回合计时重新开始，AI 座位在线且轮到它就直接落子），再向每位玩家所在节点发 `resync`，活着的 socket 立刻重新挂上并收到全量 `game_state`；崩溃节点上的玩家由客户端自动重连到任意节点，凭 token 恢复。
- **优雅退出**：SIGTERM 时先 `Handoff`——叫停本节点所有对局（不判胜负）、写最终快照、**主动释放租约**，其它节点或重启后的自己在一个扫描周期内接管，不必等租约过期；然后才关闭 socket（1001）。
- **租约丢失**：若某节点续租时发现租约已被别人拿走（它被隔离太久，已被判定死亡），立即叫停本地这份对局，保证不会两处同时下同一局。
- **欢迎帧提示**：`welcome.roomId` 告诉恢复中的客户端“你还在某局里”，前端据此把等待窗口从 1.5s 放宽到 15s，接管期间棋盘不闪回大厅；不在对局里则立刻回大厅。
- **实测**：真实进程 + 真实 Redis，`kill -9` 房主后约 6.5s 幸存节点接管，双方继续下完，9 手全部进棋谱；`SIGTERM` 重启单实例，人机对局 171ms 后恢复、AI 继续应手（`scripts/cluster-drill.sh`，`docs/evidence/cluster-drill.txt`）。Docker 里双实例 + Caddy 的同样演练见 `docs/evidence/cluster-compose.txt`，CI 的 `cluster-gate` 每次提交都跑。
- **测试**：`internal/cluster` 契约测试（内存版与 Redis 版同一套，含 20 路并发匹配不丢不重）；`internal/integration/cluster_test.go` 每个场景在“内存网络”和“miniredis”两种后端各跑一遍：跨节点对局、跨节点加入与观战、跨节点重连与顶替、房主崩溃接管、优雅交接与重启（含 AI 局）、18 人分布三节点并发匹配。
- **边界**（详见 KNOWN_ISSUES）：假设单个 Redis（Lua 脚本内拼键，不兼容 Redis Cluster 分片）；崩溃时尚未落库的最后一手会丢（快照异步写，通常 <1ms 窗口）；接管期间约 `LeaseTTL` 秒不可下棋。

## 11. 观战与棋谱（PROTOCOL §11/§12）

- **观战**：房主侧 actor 维护观战者集合，广播同时发给座位与观战者；观战人数随 `game_state.spectators` 与房间表同步。观战者开始自己的对局、断线、或对局结束时自动脱离；前端重连后自动重新观战。
- **棋谱**：actor 结束时交出完整 `Result`（座位、按序着法、胜负），房主写入 Store：Redis 中单局 30 天，`games:recent` 与每人 `pg:<id>` 两个按结束时间排序的 ZSET 索引并在同一事务里裁剪长度。HTTP 只读接口，单局不可变（长缓存），列表不缓存。前端复盘页可脱离登录直接分享。
