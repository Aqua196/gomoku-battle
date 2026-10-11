// Command server assembles and runs the M1 backend: the websocket gateway,
// health/metrics endpoints, optional pprof (PPROF=1) and optional static
// hosting of ../frontend/dist (PROTOCOL §7/§8, D8). Port: :8080, overridable
// with PORT.
package main

import (
	"log"
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

	cfg := protocol.DefaultConfig()
	st := store.NewMemStore()
	h := hub.New(st, cfg)
	srv := ws.NewServer(h, cfg, os.Getenv("ALLOW_ANY_ORIGIN") == "1")

	staticDir := findStaticDir("../frontend/dist", "frontend/dist")
	handler := srv.Handler(staticDir, os.Getenv("PPROF") == "1")

	log.Printf("gomoku-battle backend listening on :%s (static=%q pprof=%v)",
		port, staticDir, os.Getenv("PPROF") == "1")
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

// findStaticDir returns the first existing candidate so the binary works
// whether launched from backend/ or the repo root.
func findStaticDir(candidates ...string) string {
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}
