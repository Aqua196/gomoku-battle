# gomoku-battle — 实时在线五子棋对战平台

Go + WebSocket 服务端 + React/Vite 前端的多人对战五子棋。服务端权威规则、断线 30s 重连恢复、人机三档 AI（alpha-beta）、自带压测工具与一键容器部署。

## 架构

```
React SPA (lobby/game) ──WS/JSON──► Go server :8080
  (同端口托管静态资源)              ├─ ws: 升级/pump/心跳/hello+token
                                   ├─ hub(mutex): players/rooms/queue + room_list 合并广播
                                   ├─ game actor(每局1 goroutine+mailbox): 规则/计时/重连窗口
                                   ├─ ai driver: 嵌入式假客户端走同一 mailbox(三档 α-β)
                                   └─ store 接口 → MemStore | RedisStore(REDIS_ADDR 优雅降级)
```

协议见 `PROTOCOL.md`（冻结契约），并发模型与设计取舍见 `DESIGN.md`，实测性能见 `BENCHMARK.md`。

## 启动

### Docker（推荐，一条命令）

```bash
docker compose up --build
# http://localhost:8080 —— 前端由后端同端口托管；Redis 伴随启动（存会话/房间，连不上自动降级内存）
```

### 本地开发

```bash
# 后端（Go 1.27+）
cd backend && go run ./cmd/server          # :8080；PPROF=1 挂 /debug/pprof；REDIS_ADDR=host:6379 启用 Redis

# 前端（Node 20+；vite dev 代理到 8080）
cd frontend && npm ci && npm run dev       # :5173

# 生产形态：前端构建后由后端托管
cd frontend && npm run build && cd ../backend && go run ./cmd/server
```

## 演示流程

1. 开两个浏览器标签进 `http://localhost:8080`，各填昵称进大厅
2. 两边点「快速匹配」→ 自动开局；也可建房/加入房间列表
3. 轮流落子，五连判胜；认输/离开即负；断网 30s 内回来自动恢复棋局
4. 大厅点「人机对战」，选执黑 / 执白 / 猜先和难度（简单/普通/困难）即开局
5. `curl localhost:8080/metrics` 看实时指标；`docker compose logs app` 看结构化事件

## 测试与关卡

```bash
cd backend && go test -race ./...                        # 全量：规则/WS集成/AI/100房并发/Store契约(miniredis)
WS_ADDR=ws://localhost:8080/ws go test ./internal/integration -run Container -v   # 对运行中实例(容器)的全流程关卡
cd frontend && npm test                                  # vitest
cd backend && go run ./cmd/loadtest -rooms 200 -duration 60s   # 压测（需先启动服务端）
```

CI：`.github/workflows/ci.yml`（vet + race 测试 + 前端 typecheck/lint/build + compose 容器关卡）。

## 文档索引

`PROTOCOL.md` 消息协议 · `DESIGN.md` 架构/并发/取舍 · `ACCEPTANCE.md` 逐项验收证据 · `BENCHMARK.md` 压测报告 · `DECISIONS.md` 决策记录 · `KNOWN_ISSUES.md` 已知问题 · `docs/reviews/` 各阶段独立评审 · `docs/evidence/` 原始证据
