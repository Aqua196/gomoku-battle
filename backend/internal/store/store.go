// Package store abstracts persistence so MemStore and the Redis
// implementation are interchangeable for the hub. The store owns the
// durable records: player sessions (id + resume token + nick) and finished
// games for replay. Live, cluster-wide state (rooms, queue, game
// snapshots) lives in internal/cluster.
package store

// PlayerRecord is a resumable player session. Token is the opaque credential
// the client sends back in hello to resume after a disconnect.
type PlayerRecord struct {
	PlayerID string
	Token    string
	Nick     string
}

// SeatRecord is one side of a recorded game.
type SeatRecord struct {
	PlayerID string `json:"playerId"`
	Nick     string `json:"nick"`
	IsAI     bool   `json:"isAI"`
}

// GameRecord is a finished game kept for replay: both seats, every move in
// order as {x, y}, and the verdict. Winner is "black", "white" or "" (draw
// or abandoned).
type GameRecord struct {
	ID        string     `json:"id"`
	Black     SeatRecord `json:"black"`
	White     SeatRecord `json:"white"`
	Moves     [][2]int   `json:"moves"`
	Winner    string     `json:"winner"`
	Reason    string     `json:"reason"`
	StartedAt int64      `json:"startedAt"`
	EndedAt   int64      `json:"endedAt"`
}

// Retention for game records: the newest MaxRecentGames overall and the
// newest MaxPlayerGames per player stay listed.
const (
	MaxRecentGames = 200
	MaxPlayerGames = 50
)

// Store is the persistence contract.
type Store interface {
	// CreatePlayer registers a new session with a fresh id and token.
	CreatePlayer(nick string) *PlayerRecord
	// PlayerByToken finds a session by its resume token.
	PlayerByToken(token string) (*PlayerRecord, bool)
	// PlayerByID finds a session by player id.
	PlayerByID(id string) (*PlayerRecord, bool)
	// UpdateNick changes the display nick stored for a session.
	UpdateNick(playerID, nick string)
	// DeletePlayer removes a session record entirely (TTL sweep).
	DeletePlayer(playerID string)

	// SaveGame stores a finished game and indexes it under both seats.
	SaveGame(g GameRecord)
	// GameByID fetches one recorded game, moves included.
	GameByID(id string) (GameRecord, bool)
	// RecentGames lists up to limit games, newest first.
	RecentGames(limit int) []GameRecord
	// PlayerGames lists up to limit games of one player, newest first.
	PlayerGames(playerID string, limit int) []GameRecord
}
