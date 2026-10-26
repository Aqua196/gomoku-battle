package hub

import (
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
)

// Finished games for replay (PROTOCOL §12). Reads go straight to the Store;
// the hub only shapes records into the wire form.

// MaxListedGames caps one listing response.
const MaxListedGames = 50

// RecentGames lists finished games, newest first, without moves.
func (h *Hub) RecentGames(limit int) []protocol.GameRecord {
	return summaries(h.store.RecentGames(clampLimit(limit)))
}

// PlayerGames lists one player's finished games, newest first, without moves.
func (h *Hub) PlayerGames(playerID string, limit int) []protocol.GameRecord {
	return summaries(h.store.PlayerGames(playerID, clampLimit(limit)))
}

// Game returns one finished game with every move.
func (h *Hub) Game(id string) (protocol.GameRecord, bool) {
	g, ok := h.store.GameByID(id)
	if !ok {
		return protocol.GameRecord{}, false
	}
	rec := wireRecord(g)
	rec.Moves = make([]protocol.Pos, len(g.Moves))
	for i, m := range g.Moves {
		rec.Moves[i] = protocol.Pos{X: m[0], Y: m[1]}
	}
	return rec, true
}

func clampLimit(n int) int {
	if n <= 0 || n > MaxListedGames {
		return MaxListedGames
	}
	return n
}

func summaries(gs []store.GameRecord) []protocol.GameRecord {
	out := make([]protocol.GameRecord, len(gs))
	for i, g := range gs {
		out[i] = wireRecord(g)
	}
	return out
}

func wireRecord(g store.GameRecord) protocol.GameRecord {
	seat := func(s store.SeatRecord) protocol.RecordSeat {
		return protocol.RecordSeat{PlayerID: s.PlayerID, Nick: s.Nick, IsAI: s.IsAI}
	}
	var winner *string
	if g.Winner != "" {
		winner = protocol.StrPtr(g.Winner)
	}
	return protocol.GameRecord{
		ID:        g.ID,
		Black:     seat(g.Black),
		White:     seat(g.White),
		Winner:    winner,
		Reason:    g.Reason,
		MoveCount: len(g.Moves),
		StartedAt: g.StartedAt,
		EndedAt:   g.EndedAt,
	}
}
