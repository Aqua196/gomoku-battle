// Package cluster holds the state that several server instances (nodes)
// must share to act as one game server, and the message bus between them
// (DESIGN §10).
//
// Every game runs on exactly one node — its owner — inside a game.Actor;
// players may be connected to any node. The Directory answers "where is
// this player's socket", "which room is this player in", "who owns this
// room", keeps the lobby's room list, the matchmaking queue, a snapshot of
// every live game and a lease per game. The Bus carries frames and game
// commands between nodes. When an owner dies (or restarts) its leases run
// out and another node restores the games from their snapshots.
//
// Two implementations each: in-memory (one process; also used to run
// several nodes inside one test binary) and Redis (real deployments).
package cluster

import (
	"encoding/json"
	"errors"
)

// SeatInfo is one side of a room as the lobby sees it.
type SeatInfo struct {
	PlayerID  string `json:"playerId"`
	Nick      string `json:"nick"`
	IsAI      bool   `json:"isAI,omitempty"`
	Connected bool   `json:"connected"`
}

// RoomInfo is a room's cluster-wide record. Owner is the node running it.
type RoomInfo struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Status     string       `json:"status"` // waiting | playing
	IsAI       bool         `json:"isAI,omitempty"`
	CreatedAt  int64        `json:"createdAt"`
	Owner      string       `json:"owner"`
	Seats      [2]*SeatInfo `json:"seats"` // black, white; nil = open seat
	Spectators int          `json:"spectators,omitempty"`
}

// QueueEntry is one player waiting in the matchmaking queue.
type QueueEntry struct {
	PlayerID string `json:"playerId"`
	Nick     string `json:"nick"`
	Node     string `json:"node"` // where the player was connected when queueing
}

// LiveGame is the snapshot a game is restored from after its owner is
// gone: the room, both seats and every move so far. The board, the side to
// move and the move count all follow from Moves.
type LiveGame struct {
	RoomID       string      `json:"roomId"`
	Name         string      `json:"name"`
	CreatedAt    int64       `json:"createdAt"`
	StartedAt    int64       `json:"startedAt"`
	Seats        [2]SeatInfo `json:"seats"`
	Moves        [][2]int    `json:"moves"`
	AIDifficulty string      `json:"aiDifficulty,omitempty"`
}

// Errors from ClaimSeat.
var (
	ErrNoRoom   = errors.New("cluster: no such room")
	ErrRoomFull = errors.New("cluster: room is not open")
	ErrBound    = errors.New("cluster: player already in a room")
)

// Envelope kinds carried by the Bus.
const (
	KindDeliver    = "deliver"    // Frame to Player's socket
	KindMove       = "move"       // to owner: Player plays (X, Y) in Room
	KindResign     = "resign"     // to owner
	KindDisconnect = "disconnect" // to owner: Player's socket closed (Epoch fences stale ones)
	KindReconnect  = "reconnect"  // to owner: Player is back (Epoch)
	KindJoin       = "join"       // to owner: Player claimed the open seat of waiting Room
	KindWatch      = "watch"      // to owner: Player spectates Room
	KindUnwatch    = "unwatch"    // to owner
	KindSeated     = "seated"     // to Player's node: matchmaking put Player in Room (Owner)
	KindReleased   = "released"   // to Player's node: Room is over, back to the lobby
	KindWatchEnd   = "watch_end"  // to Player's node: the watched Room is over
	KindKick       = "kick"       // to Player's old node: a newer socket (Epoch) took over
	KindResync     = "resync"     // to Player's node: Room was restored on Owner, re-attach
	KindLobby      = "lobby"      // broadcast: the room list changed
)

// Envelope is one message between nodes. Fields are used per Kind.
type Envelope struct {
	Kind   string          `json:"k"`
	From   string          `json:"n,omitempty"`
	Player string          `json:"p,omitempty"`
	Room   string          `json:"r,omitempty"`
	Owner  string          `json:"o,omitempty"`
	Nick   string          `json:"nk,omitempty"`
	Epoch  int64           `json:"e,omitempty"`
	X      int             `json:"x,omitempty"`
	Y      int             `json:"y,omitempty"`
	Frame  json.RawMessage `json:"f,omitempty"`
}
