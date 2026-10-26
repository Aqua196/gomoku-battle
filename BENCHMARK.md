# BENCHMARK — 压测与 AI 引擎报告

> 所有数字均来自真实运行；原始输出存档 `docs/evidence/`。复现命令见末节。
>
> **数据修正声明（评审驱动）**：第一版数据由有缺陷的压测工具产出（连接 hello+30s 被踢死、僵尸读协程抢帧、接收判定过宽、幸存者偏差），独立评审 M3-R1~R4 实锤后工具已修复并**重跑全部档位**。本表以 `bench2-*` 修正数据为准；旧 `bench-*` 文件保留作对照，修正幅度见 §3.2。

## 1. 测试环境

| 项 | 值 |
|---|---|
| 机器 | 云虚拟机，8 vCPU / 31GB RAM（单机同跑 server + loadtest） |
| Go | 1.27.2 linux/amd64 |
| 服务端 | `PPROF=1 PORT=8080 ./gserver`（内存 Store，RoomListFlush=50ms 优化后默认） |
| 客户端 | `backend/cmd/loadtest`：真实 WS 连接（正常 pong 心跳）、create/join 确定性配对（pvp），50ms 抖动落子间隔，延迟=发 move → 收到 `lastMove==(x,y)` 且 `turn` 翻转的 game_state；sent/accepted 分开计；吞吐按真实有载窗口；60s/档 |

## 2. 结果总表（修正后，bench2-*，60s/档）

| 档位 | 连接 | 端到端连接(dial+hello) p50/p95/p99 (ms) | 落子广播 p50 | p95 | p99 | max | moves/s | 开局/完局 |
|---|---|---|---|---|---|---|---|---|
| 200 房 | 400 | 0.44* / — / — | **0.22** | **0.47** | 1.82 | — | **3969** | 2432 / 1212 |
| 500 房 | 1000 | — / — / — | **0.24** | **1.12** | 3.82 | — | **9785** | 6008 / 3072 |
| 1000 房 | ~2000 | — / — / — | **0.42** | **8.67** | 20.76 | — | **18458** | 11327 / 5708 |

\* 精确 connect_hello 分位数见各 `bench2-*.txt`；30 房复验档实测 p50 0.46/p95 7.5/p99 11.7ms。

- `docs/evidence/bench2-{200,500,1000}.txt`、`bench-analysis2.txt`、`bench2-sanity-30r.txt`
- 1000 房档未塌点：~2000 连接全活满 60s，吞吐 18458 moves/s，完局 5708；p99 20.8ms 尾部为正常内核 socket 排队。
- 完局口径：`games_finished` 仅计收到 `game_over` 的局；开局多于完局是 60s 截止时在局内未完的如实差值。
- 错误计数全部披露（`bench-analysis2.txt`）：`already_in_room`/`not_in_room` 为 game_over 与座位释放间的合法竞态，harness 退避重试并逐次计数；`conn_dead` 本轮三档为 0。

## 3. 瓶颈定位与优化

### 3.1 瓶颈（pprof 实测，500 房持续窗口 30s）

- `hub.broadcastRoomList` **420ms cum = 42.9% CPU**：每次房间变动（~10 事件/s）都在 `h.mu` 下 O(rooms) 重建全量列表 + marshal + 逐个 lobby conn 写 socket → O(rooms×events) 渲染+序列化+写风暴。heap `alloc_space` 印证：roomListMsgLocked 698MB、protoRoomLocked 224MB、JSON marshal 合计 2.0GB / 30s。
- 证据：`docs/evidence/pprof-before-cpu-top.txt`、`pprof-before-heap.pb.gz`。

### 3.2 优化：room_list 事件合并广播（RoomListFlush=50ms）

锁内只置 `rlDirty`；flusher goroutine 按 50ms 周期至多一次「渲染+marshal+推送」。测试注入 Flush=0 走同步旧路径保持确定性。

同档对比（修复后工具口径，见 `bench-analysis2.txt` 修正前后对照）：

| 指标（500 房，旧失真工具测） | 优化前 | 优化后 |
|---|---|---|
| p50 | 53.3ms | 1.01ms（−98%） |
| p95 | 402.8ms | 71.1ms（−82%） |
| avg | 112.2ms | 31.4ms（−72%） |

修正后工具下 500 房已是 p50 0.24/p95 1.12/p99 3.82ms —— 优化方向不变，绝对水位远低于旧工具所示（旧测量把自身缺陷混进了样本）。`pprof-after-cpu-top.txt`：broadcastRoomList 退出热点，剩余为内核 socket IO（Syscall6）。

### 3.3 工具缺陷修正幅度（诚实披露）

| 档 | p95 (ms) 旧→新 | p99 旧→新 | moves/s 旧→新 |
|---|---|---|---|
| 200 | 2.16 → 0.47 | 9.46 → 1.82 | 267 → 3969 (+14.9×) |
| 500 | 71.1 → 1.12 | 942.6 → 3.82 | 718 → 9785 (+13.6×) |
| 1000 | 1189 → 8.67 | 3003 → 20.76 | 945 → 18458 (+19.5×) |

旧工具三缺陷（docs/reviews/m3.md M3-R1~R4）：suppressPong 致连接 hello+30s 全灭（"60s 档"实为 ~30s 负载）；pairRun forwarder 不退出偷帧致每对≈1 局后瘫痪；接收判定无 turn 校验 + 驱逐帧幸存者偏差污染分位数。

## 4. 复现命令

```bash
cd backend && go build -o /tmp/gserver ./cmd/server && go build -o /tmp/loadtest ./cmd/loadtest
PPROF=1 PORT=8080 /tmp/gserver &
/tmp/loadtest -rooms 200 -duration 60s -out bench2-200.json
/tmp/loadtest -rooms 500 -duration 60s -out bench2-500.json
/tmp/loadtest -rooms 1000 -duration 60s -out bench2-1000.json
# pprof：
go tool pprof -top -nodecount=30 http://localhost:8080/debug/pprof/profile?seconds=30
```

## 5. 已知尾部行为与后续方向

- 单连接写阻塞（慢客户端）仍是延迟尾部主因；下一步可按连接批量写/写队列分片（M3 之外）。
- `TestConcurrent100Rooms` 历史 1 次偶发 close 1006（高负载 pong 超时）；M3 修复了一个真实 flake（双重重连断言被陈旧 presence 帧污染）。全程 `go test -race ./...` 全绿。
- room_list 合并广播为降频优化；`protoRoomLocked` 仍 O(rooms) 持锁渲染（每 tick ≤1 次），规模再上一个量级时的下一刀位置已明确（写队列分片/增量列表）。

## 6. AI 引擎（v2 对 v1）

测试机：4 vCPU（Intel Xeon 2.8GHz）。原始输出 `docs/evidence/ai-bench.txt`、`docs/evidence/ai-arena-*.txt`。

### 6.1 微基准（`go test -bench . ./internal/ai/`）

| 项 | 结果 | 说明 |
|---|---|---|
| 落子后评估：增量 | 0.92 µs | 只重扫经过该格的 4 条线 |
| 落子后评估：整盘重建 | 9.97 µs | 同一评估函数全盘重算，≈10.9× |
| 定深 4 搜索（4 个中盘局面） | v1 56.8 ms → v2 12.9 ms | 同深度同广度，≈4.4× |
| 定深 6 搜索（同上） | 67.9 ms，平均 2857 节点/次 | v1 在 1.8s 内通常到不了 6 层 |

### 6.2 对弈（`cmd/aiarena`，300ms/手，20 个随机开局 × 交换先后 = 40 局）

| 对阵 | 胜 | 负 | 和 | 胜率 | v2 平均搜索深度 |
|---|---|---|---|---|---|
| v2 hard vs v1 hard | 38 | 2 | 0 | 95% | 6.5 |
| v2 normal vs v1 normal | 28 | 12 | 0 | 70% | 4.0 |
| v2 easy vs v1 easy | 18 | 21 | 1 | 45% | 2.0 |
| v2 hard vs v2 normal | 31 | 9 | 0 | 78% | 5.9 |
| v2 normal vs v2 easy | 36 | 4 | 0 | 90% | 4.0 |

- 搜索有时间预算，结果随机器负载有 ±1~2 局波动（多次运行 hard 对 v1 hard 在 95%~98%）。
- easy 刻意保持与 v1 同档（2 层 + 噪声、无威胁求解），用来给新手赢。
- v2 hard 的落败局几乎都是执白（自由规则下黑棋先手优势明显）。

复现：

```bash
cd backend
go test -run XXX -bench . -benchtime 3s ./internal/ai/
go run ./cmd/aiarena -openings 20 -think 300ms -a hard -b v1:hard    # -b normal 则与 v2 自身其他难度对弈
go run ./cmd/aiarena -openings 5 -think 300ms -trace                 # 打印 v2 落败局的逐手记录
```
