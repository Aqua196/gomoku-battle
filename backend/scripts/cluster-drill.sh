#!/usr/bin/env bash
# Failover drills against real processes (DESIGN §10):
#   1. two nodes on one Redis, a cross-node game, the owner killed with
#      SIGKILL — the survivor adopts the game when the lease lapses;
#   2. one node restarted with SIGTERM mid AI game — graceful handoff, the
#      game is back as soon as the new process is up.
# Needs redis-server on PATH. Usage: scripts/cluster-drill.sh  (from backend/)
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
port=$((20000 + RANDOM % 20000))
cleanup() {
  for f in "$work"/*.pid; do [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null || true; done
  redis-cli -p "$port" shutdown nosave >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

redis-server --port "$port" --save "" --appendonly no --daemonize yes --logfile "$work/redis.log"
go build -o "$work/server" ./cmd/server

cat > "$work/node.sh" <<NODE
#!/usr/bin/env bash
echo \$\$ > "$work/\$1.pid"
exec env NODE_ID=\$1 PORT=\$2 REDIS_ADDR=localhost:$port MAX_CONNS_PER_IP=0 ALLOW_ANY_ORIGIN=1 "$work/server" >> "$work/\$1.log" 2>&1
NODE
chmod +x "$work/node.sh"
start() { nohup "$work/node.sh" "$1" "$2" >/dev/null 2>&1 & }
wait_up() { until curl -fs "localhost:$1/healthz" >/dev/null; do sleep 0.1; done; }

start a 18081
start b 18082
wait_up 18081
wait_up 18082

echo "== drill 1: owner crash (SIGKILL)"
DRILL_A=ws://localhost:18081/ws DRILL_B=ws://localhost:18082/ws \
DRILL_KILL_B="kill -9 \$(cat $work/b.pid)" \
  go test -count=1 -v -run 'TestClusterDrill$' ./internal/integration/ | grep -E 'drill_test|^(---|ok|FAIL)'

cat > "$work/restart-a.sh" <<RESTART
#!/usr/bin/env bash
pid=\$(cat "$work/a.pid")
kill -TERM "\$pid"
while kill -0 "\$pid" 2>/dev/null; do sleep 0.05; done
nohup "$work/node.sh" a 18081 >/dev/null 2>&1 &
RESTART
chmod +x "$work/restart-a.sh"

echo "== drill 2: graceful restart (SIGTERM) with an AI game"
DRILL_A=ws://localhost:18081/ws DRILL_RESTART_A="$work/restart-a.sh" \
  go test -count=1 -v -run 'TestRestartDrill$' ./internal/integration/ | grep -E 'drill_test|^(---|ok|FAIL)'
