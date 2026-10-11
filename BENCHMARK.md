# BENCHMARK — M3 压测报告

> 所有数字均来自真实运行；原始输出存档 `docs/evidence/`（JSON 全量 + pprof 文本）。复现命令见末节。

## 1. 测试环境

| 项 | 值 |
|---|---|
| 机器 | 云虚拟机，8 vCPU / 31GB RAM（单机同跑 server + loadtest + 浏览器） |
| Go | 1.27.2 linux/amd64 |
| 服务端 | `PPROF=1 PORT=8080 ./gserver`（内存 Store，RoomListFlush=50ms 为优化后默认） |
| 客户端 | `backend/cmd/loadtest`：真实 WS 连接、create/join 确定性配对（pvp），抖动落子间隔，lastMove 匹配法测广播延迟（发 move → 收到含该手的 game_state） |

## 2. 结果总表（60s/档）

| 档位 | 连接 | 连接建立 p50/p95/p99 (ms) | 落子广播 p50 | p95 | p99 | max | moves/s | 完成局数 |
|---|---|---|---|---|---|---|---|---|
| 200 房（优化后） | 400 | 0.23 / 2.2 / 9.5 | 0.23 | 2.2 | 9.5 | 36 | 267 | — |
| 500 房 **优化前** | 1000 | — | **53.3** | **402.8** | 682.6 | 1518 | 658 | — |
| 500 房 **优化后** | 1000 | — | **1.01** | **71.1** | 942.6 | 5312 | 718 | 591 |
| 1000 房（优化后） | 1999/2000 | — | 9.1 | 1189 | 3003 | 6506 | 945 | — |

- `docs/evidence/bench-{200,500-before,500-after,1000}.txt`、`bench-analysis.txt`
- 1000 房档未塌点：1999/2000 连接拨通，吞吐 945 moves/s；尾部延迟劣化主要来自内核 socket IO（优化后 profile 顶部为 `Syscall6`）。
- loadtest 端错误计数（illegal_move/not_your_turn）为压测节奏产物：配对双方同时尝试落子、或上一手已终局仍发子，服务端正确拒绝——属预期协议行为，非服务端缺陷。

## 3. 瓶颈定位（pprof 实测）

500 房持续窗口 30s CPU profile（`pprof-before-cpu-top.txt`）：

- `hub.broadcastRoomList` **420ms cum = 42.9%**：每次房间变动（create/join/finish，~10 事件/s）都在 `h.mu` 下 `protoRoomLocked` O(rooms) 重建全量列表 + `protocol.Marshal` + 逐个 lobby conn 写 socket → O(rooms×events) 渲染+序列化 + 写风暴。
- heap `alloc_space` 印证：roomListMsgLocked 698MB、protoRoomLocked 224MB、JSON marshal 合计 2.0GB / 30s（总 4.3GB）。

## 4. 优化：room_list 事件合并广播

- `broadcastRoomList` 改为：锁内只置 `rlDirty` 脏位；独立 flusher goroutine 按 `RoomListFlush=50ms` 周期至多产出一次「渲染+marshal+推送」。~20 事件/s 合并为 ≤1 次广播/50ms。连接层原有的 per-conn 脏位合并（M1）叠加生效。
- 可注入参数：测试注入 `RoomListFlush=0` 走同步旧路径，保持确定性。

同档复测对比（500 房，同参数 60s）：

| 指标 | 优化前 | 优化后 | 变化 |
|---|---|---|---|
| p50 | 53.3ms | 1.01ms | **−98%** |
| p95 | 402.8ms | 71.1ms | **−82%** |
| avg | 112.2ms | 31.4ms | **−72%** |
| p99 | 682.6ms | 942.6ms | +38%（尾部回退：广播成本移除后露出连接写阻塞，见 pprof-after 顶部 Syscall6） |

`pprof-after-cpu-top.txt`：broadcastRoomList 已退出热点；瓶颈转移为内核 socket 写。

## 5. 复现命令

```bash
cd backend && go build -o /tmp/gserver ./cmd/server && go build -o /tmp/loadtest ./cmd/loadtest
PPROF=1 PORT=8080 /tmp/gserver &                 # 服务端
/tmp/loadtest -rooms 200 -duration 60s -out bench-200.json
/tmp/loadtest -rooms 500 -duration 60s -out bench-500.json
/tmp/loadtest -rooms 1000 -duration 60s -out bench-1000.json
# pprof（服务端运行时）：
go tool pprof -top -nodecount=30 http://localhost:8080/debug/pprof/profile?seconds=30
```

合入后复验：`/tmp/loadtest -rooms 20 -duration 10s` → `docs/evidence/bench-sanity-20r.json`（40 conns、p50 0.33ms）。

## 6. 已知尾部行为

- p99 在 500/1000 档的劣化路径是单连接写阻塞（慢客户端 socket），下一步优化方向：按连接批量写/写队列分片（未做，属 M3 之外）。
- `TestConcurrent100Rooms` 历史上 1 次偶发 close 1006（高负载 pong 超时踢除）；M3 顺带修复了一个真实 flake（双重重连断言被陈旧 presence 帧污染）。压测全程 `go test -race ./...` 全绿。
