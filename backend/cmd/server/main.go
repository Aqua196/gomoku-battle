// Command server assembles and runs the backend: the websocket gateway,
// health/metrics endpoints, optional pprof (PPROF=1), optional static
// hosting of ../frontend/dist (PROTOCOL §7/§8, D8), and the pluggable
// store — Redis when REDIS_ADDR is set and reachable, MemStore otherwise
// (the fallback is logged, never silent). Port: :8080 via PORT.
//
// SIGINT/SIGTERM shut down gracefully: stop accepting, send every websocket
// a 1001 close so clients reconnect, wait for in-flight bookkeeping.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
	"github.com/Aqua196/gomoku-battle/backend/internal/ws"
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

	st := store.Store(store.NewMemStore())
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		if rdb, err := store.NewRedisStore(addr); err == nil {
			st = rdb
			slog.Info("store: redis", "addr", addr)
		} else {
			slog.Warn("store: redis unreachable, falling back to in-memory",
				"addr", addr, "err", err)
		}
	}

	cfg := protocol.DefaultConfig()
	cfg.TrustProxy = os.Getenv("TRUST_PROXY") == "1"
	if v, ok := envInt("MAX_CONNS_PER_IP"); ok {
		cfg.MaxConnsPerIP = v // 0 disables (load tests open thousands from one host)
	}
	h := hub.New(st, cfg)
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
	sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(sctx); err != nil {
		slog.Warn("http shutdown", "err", err)
	}
	if err := srv.Shutdown(sctx); err != nil {
		slog.Warn("websocket drain", "err", err)
	}
	h.Stop()
	slog.Info("bye")
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
