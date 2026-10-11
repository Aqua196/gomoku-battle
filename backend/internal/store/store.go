// Package store abstracts persistence so M4 can swap MemStore for a Redis
// implementation without touching hub/game code. The store owns durable-ish
// records: player sessions (id + resume token + nick) and the room registry
// (metadata only — runtime seats live in the hub).
package store

// PlayerRecord is a resumable player session. Token is the opaque credential
// the client sends back in hello to resume after a disconnect.
type PlayerRecord struct {
	PlayerID string
	Token    string
	Nick     string
}

// RoomRecord is the room registry metadata. Seat assignments and the live
// game actor are runtime concerns and stay in the hub.
type RoomRecord struct {
	ID        string
	Name      string
	Status    string // waiting|playing|finished
	IsAI      bool
	CreatedAt int64
}

// Store is the persistence contract for M1.
type Store interface {
	// CreatePlayer registers a new session with a fresh id and token.
	CreatePlayer(nick string) *PlayerRecord
	// PlayerByToken finds a session by its resume token.
	PlayerByToken(token string) (*PlayerRecord, bool)
	// PlayerByID finds a session by player id.
	PlayerByID(id string) (*PlayerRecord, bool)
	// UpdateNick changes the display nick stored for a session.
	UpdateNick(playerID, nick string)

	// UpsertRoom inserts or replaces a room record.
	UpsertRoom(r RoomRecord)
	// RoomByID fetches a room record.
	RoomByID(id string) (RoomRecord, bool)
	// DeleteRoom removes a room record.
	DeleteRoom(id string)
	// ListRooms returns all room records (any status).
	ListRooms() []RoomRecord
}
