# DESIGN — 实时在线五子棋对战平台

> 架构文档。随里程碑更新；协议细节见 PROTOCOL.md（冻结契约）。

## 1. 总体架构

```
┌────────────┐   WS/JSON (PROTOCOL.md)   ┌───────────────────────────────┐
│  React SPA │ ◄───────────────────────► │            Go server          │
│ lobby/game │                           │  ┌─────────┐  ┌────────────┐  │
└────────────┘                           │  │ Client  │  │ Client     │  │
        HTTP (静态资源/metrics/healthz)   │  │ conn #1 │  │ conn #2 .. │  │
                                         │  │ rd+wr   │  │ rd+wr pump │  │
                                         │  └────┬────┘  └─────┬──────┘  │
                                         │       ▼             ▼         │
                                         │  ┌─────────────────────────┐  │
                                         │  │ Hub (mutex 保护的注册表) │  │
                                         │  │ players / rooms / queue │  │
                                         │  └───────────┬─────────────┘  │
                                         │              │ 派发事件         │
                                         │   ┌──────────▼───────────┐    │
                                         │   │ Game actor (每局一个 │    │
                                         │   │ goroutine + mailbox) │    │
                                         │   │ 权威规则/计时/重连窗口 │   │
                                         │   └──────────┬───────────┘    │
                                         │              ▼                │
                                         │   Store 接口 → MemStore(M1)   │
                                         │              → RedisStore(M4) │
                                         │   AI engine (M2, mailbox 投喂) │
                                         └───────────────────────────────┘
```

## 2. 模块划分

| 模块 | 职责 |
|---|---|
| `internal/protocol` | 消息类型、常量（超时/窗口，可注入测试值）、错误码 |
| `internal/game` | 五子棋规则纯逻辑：落子校验、四方向胜负、平局；Game actor：回合计时、断线窗口、事件邮箱 |
| `internal/hub` | 玩家注册表、房间表、匹配队列（FIFO）、大厅广播 |
| `internal/ws`（或 server） | WS 升级、readPump/writePump、心跳、hello 绑定 |
| `internal/store` | `Store` 接口（Player/Session/Room 持久化）+ `MemStore` |
| `internal/ai`（M2） | 评估函数 + alpha-beta + 难度配置 + 超时截断 |
| `cmd/server` | 组装、HTTP 路由、静态资源托管 |
| `cmd/wsclient` | 脚本化 WS 客户端（集成测试与 M3 压测共用） |
| `cmd/loadtest`（M3） | 并发压测：连接耗时、广播延迟 P50/95/99、资源占用 |

## 3. 并发模型（选型已定）

- **每连接 2 goroutine**（readPump / writePump），下行经带缓冲 channel（SEND_BUF=64，满则判慢消费断开）。
- **每局 1 actor goroutine**：落子/认输/断线/重连/tick 全部串行进 mailbox → 对局状态无锁、无竞态。计时器用 `time.Timer` 在 actor 内 select。
- **Hub 用互斥锁保护的注册表**（players/rooms/queue），持锁期间**不发送网络消息**（投递只写各连接的缓冲 channel，非阻塞 or 判慢踢出）。
- 备选方案：全局单事件循环（更简单但单点吞吐受限）、每玩家 actor（房间状态要拆两处）。选 mutex-Hub + per-game actor：对局热路径无锁，跨房间操作极少。

## 4. 状态机

### 玩家

`connecting →(hello)→ lobby →(create/join/matchmake/play_ai)→ in_room_waiting → playing →(game_over)→ lobby`；任何状态断线 → `disconnected`（对局中启动 30s 判负计时；大厅中直接清出队列/座位）。

### 房间

`waiting →(满座)→ playing →(game_over)→ finished→删除`。waiting 房主离开 → 房间删除。

### 对局

`playing{turn, deadlines} → finished{winner, reason}`。

## 5. 关键取舍（详见 DECISIONS.md）

- 全量 GameState 广播而非增量（225 cell ≤1KB，简单幂等，重连与正常流同构）。
- 服务端权威：客户端只做展示与输入，杜绝信任边界问题。
- token 会话恢复：重连实现只依赖内存会话表，天然可测；M4 换 Redis 即获得跨重启恢复。
- AI 走同一 actor mailbox（M2）：AI = 一个内嵌“假客户端”，不引入第二条协议路径。
