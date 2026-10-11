# PROTOCOL — WebSocket 协议契约（冻结）

> 本文件是前后端并行的唯一契约。任何偏差都视为 bug；如确需变更，须先修改本文件，再同步前后端实现。
> 版本：v1（M1）

## 0. 总约定

- 传输：`ws://<host>/ws`，JSON text frame，每条消息是一个 object，必含 `"type"` 字段。
- 时间戳一律 Unix 毫秒（int64）。
- 棋盘：15×15，`board[y][x]`，`x`=列(0–14)，`y`=行(0–14)。格子值：`0`=空，`1`=黑，`2`=白。
- 颜色字段统一用字符串 `"black" | "white"`。
- 所有服务端→客户端消息均可被广播（除标注“仅发送者”外）。
- 未知 `type` 的消息：接收方忽略；格式非法回复 `error{code:"bad_request"}`。

## 1. 连接、身份与心跳

### hello / welcome

客户端连接建立后 **5s 内**必须发送：

```json
{"type":"hello","nick":"string(1-32)","token":"可选，断线重连凭证"}
// nick 经 TrimSpace 后按 rune 计 1–32 字符；纯空白非法
```

服务端回：

```json
{"type":"welcome","playerId":"p_xxx","token":"<opaque>","nick":"<生效昵称>","resumed":false}
```

- `token` 为服务端生成的不可猜随机串（≥16B 熵）。客户端应存入 localStorage 供重连用。
- 若 `hello.token` 命中一个**已断线**的玩家会话：绑定新连接到该 `playerId`，`resumed=true`；若该玩家在存活对局中，随后立即下发一条 `game_state`；若无存活对局，客户端必须在短超时（~1.5s）后自行清空本地对局视图回到大厅。
- 若 `hello.token` 命中**仍在线**的会话：**顶替（takeover）**——服务端立即关闭旧连接、把会话绑定到新连接，`resumed=true`（token 即凭证；多开会话后者赢）。
- `welcome` 携带 `"serverNow":<ts>` 供客户端估时钟偏差。
- 超时未 hello → `error{code:"hello_timeout"}` 后关闭。

### 心跳

- WS 层：服务端每 **10s** 发 ping，**30s** 未收到 pong → 关闭连接并按“断线”处理。
- 应用层（可选，供前端显示延迟）：`{"type":"ping"}` → `{"type":"pong","ts":<echo或服务器当前ts>}`。

## 2. 大厅（lobby）

已通过 hello 且**不在进行中房间**的玩家处于 lobby 状态。

| C→S | S→C | 说明 |
|---|---|---|
| `{"type":"create_room","name":"..."}` | `{"type":"room","action":"created","room":{Room}}` | 创建者自动入座黑方 |
| `{"type":"join_room","roomId":"r_x"}` | `{"type":"room","action":"joined","room":{Room}}` | 加入者入座白方；满员即 `game_start` |
| `{"type":"leave_room"}` | `{"type":"room","action":"left","roomId":"r_x"}` | waiting 房间离开=退出座位；**playing 中离开=认输** |
| `{"type":"list_rooms"}` | `{"type":"room_list","rooms":[Room...]}` | 主动拉取 |
| `{"type":"matchmake"}` | `{"type":"matchmaking","status":"queued","position":1}` | 进入匹配队列 |
| `{"type":"matchmake_cancel"}` | `{"type":"matchmaking","status":"cancelled"}` | 退出队列 |
| `{"type":"play_ai","difficulty":"easy\|normal\|hard"}` | `game_start`（M2） | M1 服务端回复 `error{code:"not_implemented"}` |
| `{"type":"resign"}` | — | 对局中认输（见 §4） |

- 房间列表变化时，服务端向**所有 lobby 玩家**推送 `room_list`（全量）。实现要求：同一连接的 `room_list` 推送必须**合并**（发送缓冲内只保留最新一份，脏位合并），防止 churn 期间正常客户端被误判慢消费踢出。
- 匹配成功：服务端创建 `isAI=false` 的房间，双方收到 `game_start`（配对成功者**不再**收到 `matchmaking{queued}`）。队列按 FIFO 两两配对。
- 队列中的玩家执行 `create_room`/`join_room`/`play_ai` 会被隐式出队，服务端回 `matchmaking{status:"cancelled"}`。
- 房间名长度 ≤64；缺省 `"<nick>'s room"`。

```jsonc
// Room
{"id":"r_x","name":"...","status":"waiting|playing|finished",
 "players":[{"playerId":"p_x","nick":"...","color":"black|white|null","connected":true}],
 "isAI":false,"createdAt":1730000000000}
```

错误：`room_not_found` `room_full` `already_in_room` `not_in_room` `not_in_lobby`。

## 3. 对局消息

### game_start（按接收方个性化 `youColor`）

```jsonc
{"type":"game_start","game":{GameState},"youColor":"black|white"}
```

### move / game_state

```jsonc
{"type":"move","x":7,"y":7}                 // C→S
{"type":"game_state","game":{GameState}}    // S→C，每次合法落子后向双方广播（全量状态，不用增量）
```

非法落子仅回复发送者 `error`，code ∈ `not_your_turn | illegal_move | game_over | not_in_room`。

### game_over

```jsonc
{"type":"game_over","reason":"five|draw|timeout|resign|disconnect_timeout|abandoned","winner":"black|white|null","game":{GameState}}
```

- `winner:null` 仅当 `reason:"draw"`（平局）或 `reason:"abandoned"`（双方弃局，无负方）。客户端不得按 `winner===null` 直接判平局。
- game_over 后房间删除，双方回到 lobby 状态；随后可向双方推一次 `room_list`。

### opponent_presence

对手断线/重连时推给另一方：

```jsonc
{"type":"opponent_presence","connected":false,"deadline":1730000030000}
```

`connected:true` 时 `deadline:null`。

### GameState

```jsonc
{
 "roomId":"r_x",
 "status":"playing|finished",
 "board":[[0,...]x15 ...],
 "turn":"black|white",
 "moveNumber":3,
 "lastMove":{"x":7,"y":7}|null,
 "turnDeadline":1730000030000,       // 当前回合落子截止 ts；status!=playing 时 null
 "reconnectDeadline":null,           // 有玩家断线时为判负截止 ts，否则 null
 "players":{
   "black":{"playerId":"p_x","nick":"...","connected":true,"isAI":false},
   "white":{"playerId":"p_y","nick":"...","connected":true,"isAI":false}
 },
 "winner":null,"winReason":null,
 "serverNow":1730000000000
}
```

## 4. 规则与判定（服务端权威）

- 黑先。15×15 自由规则（free-style）：任意方向**连续 ≥5 子**即胜（长连也算胜，无禁手）。
- 胜负判定四方向：横、竖、主斜、副斜，以新落子为锚点向两侧计数。
- 满盘 225 手无胜 → `draw`。
- **回合超时**：每回合 `MOVE_TIMEOUT=30s`，`turnDeadline` 随每手刷新；超时者负，`reason:"timeout"`。
- **断线**：对局内连接丢失（见 §5），`reason:"disconnect_timeout"`。断线恰逢己方回合时 `turnDeadline` 与 `reconnectDeadline` 并行，终局 reason 依计时器先到者可为 `timeout` 或 `disconnect_timeout`（判定结果一致，文案需预期双理由）。
- **认输**：`resign` 或 playing 中 `leave_room`，`reason:"resign"`。
- **双方弃局**：双断线挂起后超 AbandonTTL 无人归，`reason:"abandoned"`、`winner:null`。

## 5. 断线与重连

- 对局内任何一方断线：设置 `reconnectDeadline = 断线时刻 + RECONNECT_WINDOW(30s)`，向在线方推 `opponent_presence{connected:false,deadline}`。
- 断线方在窗口内用 `hello{token}` 重连：恢复会话与对局视图（welcome.resumed + game_state），清除 `reconnectDeadline`，向对手推 `connected:true`。
- 窗口到期未回：断线方判负 `disconnect_timeout`。
- 每次断线重新计 30s 窗口（允许多次重连，各计各的）。
- `reconnectDeadline` 与 `turnDeadline` 独立计时，先到先生效。**竞先裁决**：计时器触发时必须先排空 mailbox 中已到达的事件再判负（按时到达的落子/重连不得被 select 随机性丢弃）。
- **双方同时断线**：判负裁决挂起——任一方重连时再结算：重连方自身窗口已过 → 重连方负；否则若对手窗口已过 → 对手负；否则对局继续。双方都永远不回则对局挂起至会话过期清理。
- 断线时若轮到断线方，`turnDeadline` 照常走；若轮到在线方，在线方仍须在 `turnDeadline` 内落子。

## 6. 时间常量（服务端实现为可配置参数，测试可缩短）

| 名称 | 值 | 含义 |
|---|---|---|
| HELLO_TIMEOUT | 5s | 连接后 hello 截止 |
| WS_PING_PERIOD | 10s | ping 间隔 |
| WS_PONG_WAIT | 30s | pong 容忍 |
| WRITE_WAIT | 10s | 单次写截止 |
| MOVE_TIMEOUT | 30s | 单回合限时 |
| RECONNECT_WINDOW | 30s | 断线判负窗口 |
| SEND_BUF | 64 msgs | 每连接下行缓冲，满则按断线处理 |

## 7. HTTP 端点（同一端口）

- `GET /healthz` → `200 "ok"`
- `GET /metrics` → `{"connections":int,"lobbyPlayers":int,"rooms":int,"activeGames":int,"gamesPlayed":int,"totalMoves":int,"movesPerSec":float,"aiGames":int}`
  - `movesPerSec`：trailing-60s 窗口内接受落子数 / 60；`aiGames`：人机局启动累计（≠完成数）；其余为瞬时快照。
- `/debug/pprof/*`：仅当环境变量 `PPROF=1` 时挂载
- 生产模式后端直接托管 `frontend/dist` 静态资源（SPA fallback 到 index.html）；开发模式用 vite proxy 转发 `/ws` `/healthz` `/metrics`，无需 CORS。

## 8. 错误码一览（`{"type":"error","code":"...","message":"人类可读"}`）

`bad_request` `hello_timeout` `unauthorized` `not_in_lobby` `already_in_room` `not_in_room` `room_not_found` `room_full` `not_your_turn` `illegal_move` `game_over` `invalid_difficulty` `not_implemented` `internal`

## 9. 前端行为约定

- 两个页面：大厅 `/`（昵称输入→房间列表/建房/快速匹配/人机入口）与棋盘 `/game`（棋盘渲染、轮到谁、倒计时、对手在线状态、认输、对局结束弹层→返回大厅）。
- token/nick 存 localStorage；WS 断开自动指数退避重连并在 hello 中带 token。
- 棋盘 15×15 CSS grid 或 SVG 均可；必须显示 `lastMove` 高亮与剩余时间。

## 10. AI 对局约定（M2）

- `play_ai{difficulty}`，`difficulty ∈ "easy"|"normal"|"hard"`；非法值回 `error{code:"invalid_difficulty"}`；已在房间回 `already_in_room`。
- 座位固定：**真人恒执黑先手，AI 恒执白**。房间内 AI 为伪玩家：`players.white = {playerId:"ai_*", nick:"AI (difficulty)", connected:true, isAI:true}`（`connected` 恒真，为渲染值）。
- isAI 房间出现在 `room_list`（与人人房间同构，status=playing）。
- AI 思考预算 ≤2s/手（hard 档内迭代深化至 ~1.8s 自限，超时返回当前最优解）。
- AI 落子与真人同走 `move` 校验与广播路径；真人轮到 AI 回合时发 `move` 被拒 `not_your_turn`。
- 人机对局中真人断线/认输的判负、重连窗口规则与人人对局完全一致。
