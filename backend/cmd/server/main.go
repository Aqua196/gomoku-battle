// Command server assembles and runs the backend: the websocket gateway,
// health/metrics endpoints, optional pprof (PPROF=1), optional static
// hosting of ../frontend/dist (PROTOCOL §7/§8, D8), and the pluggable
// store — Redis when REDIS_ADDR is set and reachable, MemStore otherwise
// (the fallback is logged, never silent). Port: :8080 via PORT.
package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
	"github.com/Aqua196/gomoku-battle/backend/internal/ws"
)

func main() {
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
	h := hub.New(st, cfg)
	srv := ws.NewServer(h, cfg, os.Getenv("ALLOW_ANY_ORIGIN") == "1")

	staticDir := findStaticDir("../frontend/dist", "frontend/dist")
	handler := srv.Handler(staticDir, os.Getenv("PPROF") == "1")

	slog.Info("gomoku-battle backend listening",
		"port", port, "static", staticDir, "pprof", os.Getenv("PPROF") == "1")
	log.Fatal(http.ListenAndServe(":"+port, handler))
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
