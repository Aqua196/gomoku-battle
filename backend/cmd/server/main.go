// Command server assembles and runs the backend: the websocket gateway,
// health/metrics endpoints, optional pprof (PPROF=1), optional static
// hosting of ../frontend/dist (PROTOCOL §7/§8, D8), and its state:
//
//   - REDIS_ADDR set and reachable: sessions and records in Redis, and the
//     node joins the cluster on that Redis (DESIGN §10) — any number of
//     instances behind a load balancer act as one server, games survive a
//     restart or a crashed instance. NODE_ID names the node (default:
//     hostname plus a random suffix).
//   - otherwise: one in-memory process (the fallback is logged, never
//     silent).
//
// SIGINT/SIGTERM shut down gracefully: hand owned games off (cluster mode),
// stop accepting, send every websocket a 1001 close so clients reconnect,
// wait for in-flight bookkeeping. Port: :8080 via PORT.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/cluster"
	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
	"github.com/Aqua196/gomoku-battle/backend/internal/ws"
	"github.com/redis/go-redis/v9"
)

// shutdownGrace bounds how long a shutdown waits for connections to drain.
const shutdownGrace = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	cfg := protocol.DefaultConfig()
	cfg.TrustProxy = os.Getenv("TRUST_PROXY") == "1"
	if v, ok := envInt("MAX_CONNS_PER_IP"); ok {
		cfg.MaxConnsPerIP = v // 0 disables (load tests open thousands from one host)
	}

	var h *hub.Hub
	clustered := false
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		if st, err := store.NewRedisStore(addr); err == nil {
			rdb := redis.NewClient(&redis.Options{Addr: addr})
			node := nodeID()
			h = hub.NewNode(st, cfg, hub.Options{
				Node: node,
				Dir:  cluster.NewRedisDirectory(rdb),
				Bus:  cluster.NewRedisBus(rdb, node),
			})
			clustered = true
			slog.Info("state: redis cluster", "addr", addr, "node", node)
		} else {
			slog.Warn("state: redis unreachable, falling back to in-memory",
				"addr", addr, "err", err)
		}
	}
	if h == nil {
		cfg.LeaseTTL = 0 // nothing outlives this process: no snapshots, no leases
		h = hub.New(store.NewMemStore(), cfg)
	}
	srv := ws.NewServer(h, cfg, os.Getenv("ALLOW_ANY_ORIGIN") == "1")

	staticDir := findStaticDir("../frontend/dist", "frontend/dist")
	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(staticDir, os.Getenv("PPROF") == "1"),
		ReadHeaderTimeout: 10 * time.Second, // slowloris guard
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 16,
	}

	slog.Info("gomoku-battle backend listening",
		"port", port, "static", staticDir, "pprof", os.Getenv("PPROF") == "1")
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "err", err)
			os.Exit(1)
		}
		return
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	if clustered {
		h.Handoff() // games resume on another node (or this one restarted) at once
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(sctx); err != nil {
		slog.Warn("http shutdown", "err", err)
	}
	if err := srv.Shutdown(sctx); err != nil {
		slog.Warn("websocket drain", "err", err)
	}
	h.Close()
	slog.Info("bye")
}

// nodeID names this process in the cluster: NODE_ID, else the hostname
// (the container id under compose) plus a random suffix, unique per start.
func nodeID() string {
	if id := os.Getenv("NODE_ID"); id != "" {
		return id
	}
	host, err := os.Hostname()
	if err != nil {
		host = "node"
	}
	var b [3]byte
	_, _ = rand.Read(b[:])
	return host + "-" + hex.EncodeToString(b[:])
}

// envInt reads a non-negative integer environment variable.
func envInt(name string) (int, bool) {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// findStaticDir returns the first existing candidate so the binary works
// whether launched from backend/ or the repo root (or the image's /app).
func findStaticDir(candidates ...string) string {
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}
