package ws

import (
	"net/http"
	"strconv"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

// Read-only JSON API over finished games (PROTOCOL §12). A record never
// changes once written, so single games are cacheable forever; listings
// change with every finished game and are not cached.

func (s *Server) handleRecentGames(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, "no-store", protocol.GameList{Games: s.hub.RecentGames(limitParam(r))})
}

func (s *Server) handlePlayerGames(w http.ResponseWriter, r *http.Request) {
	games := s.hub.PlayerGames(r.PathValue("id"), limitParam(r))
	writeJSON(w, http.StatusOK, "no-store", protocol.GameList{Games: games})
}

func (s *Server) handleGame(w http.ResponseWriter, r *http.Request) {
	g, ok := s.hub.Game(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, "no-store", protocol.NewError(protocol.ERoomNotFound, "no such game"))
		return
	}
	writeJSON(w, http.StatusOK, "public, max-age=31536000, immutable", g)
}

func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n // the hub clamps 0 / out-of-range to its maximum
}

func writeJSON(w http.ResponseWriter, status int, cache string, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cache)
	w.WriteHeader(status)
	// encoding/json escapes <, > and & and the type is pinned to JSON, so
	// user-chosen nicks cannot become markup.
	_, _ = w.Write(protocol.Marshal(v)) //nolint:gosec // see above
}
