// Package hub is the mutex-protected registry at the center of the server:
// players, rooms, the FIFO matchmaking queue and the lobby broadcast
// (DESIGN §2/§3). It never sends network traffic while holding its lock —
// every outbound write is a non-blocking TrySend into a connection's
// buffered channel, and every game-actor post happens after the lock is
// released, so there is no lock ordering with actor code.
package hub

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"sync/atomic"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
)

// Conn is the hub's view of one websocket connection: a buffered, never
// blocking sink plus a kill switch for slow consumers (PROTOCOL §6 SEND_BUF).
type Conn interface {
	// Send enqueues one marshaled frame; false means the buffer is full and
	// the connection is being dropped as a slow consumer.
	Send(data []byte) bool
	// Close terminates the connection.
	Close()
}

// PlayerState mirrors the player state machine in DESIGN §4. A disconnected
// player keeps its state (Playing survives across the reconnect window) but
// has conn == nil.
type PlayerState int

const (
	StateLobby   PlayerState = iota // identified, not in a room (queueing lives here)
	StateWaiting                    // seated in a waiting room
	StatePlaying                    // in a live game
)

// Player is the runtime identity bound to a store.PlayerRecord session.
type Player struct {
	rec    *store.PlayerRecord
	conn   Conn
	state  PlayerState
	roomID string
	queued bool
}

// ID returns the player id.
func (p *Player) ID() string { return p.rec.PlayerID }

// Nick returns the current display nick.
func (p *Player) Nick() string { return p.rec.Nick }

// Token returns the resume token.
func (p *Player) Token() string { return p.rec.Token }

// roomEntry adds runtime seats and the game actor to a store.RoomRecord.
// seats[0] is black, seats[1] is white.
type roomEntry struct {
	meta  store.RoomRecord
	seats [2]*Player
	actor *game.Actor
}

// Metrics is the /metrics JSON payload (PROTOCOL §7).
type Metrics struct {
	Connections  int `json:"connections"`
	LobbyPlayers int `json:"lobbyPlayers"`
	Rooms        int `json:"rooms"`
	ActiveGames  int `json:"activeGames"`
	GamesPlayed  int `json:"gamesPlayed"`
}

// Hub owns the player/room/queue registries.
type Hub struct {
	cfg   protocol.Config
	store store.Store

	mu      sync.Mutex
	players map[string]*Player // by player id; disconnected players stay resumable
	rooms   map[string]*roomEntry
	queue   []*Player // FIFO

	connCount   atomic.Int64
	gamesPlayed atomic.Int64
}

// New builds a hub around a Store and an injected timing config.
func New(st store.Store, cfg protocol.Config) *Hub {
	return &Hub{
		cfg:     cfg,
		store:   st,
		players: make(map[string]*Player),
		rooms:   make(map[string]*roomEntry),
	}
}

// ConnOpened counts a freshly accepted socket for metrics.
func (h *Hub) ConnOpened() { h.connCount.Add(1) }

// ConnClosed counts a socket that finished its read loop for metrics.
func (h *Hub) ConnClosed() { h.connCount.Add(-1) }

// Metrics snapshots the counters for /metrics.
func (h *Hub) Metrics() Metrics {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := Metrics{
		Connections: int(h.connCount.Load()),
		Rooms:       len(h.rooms),
		GamesPlayed: int(h.gamesPlayed.Load()),
	}
	for _, p := range h.players {
		if p.conn != nil && p.state != StatePlaying {
			m.LobbyPlayers++
		}
	}
	for _, e := range h.rooms {
		if e.meta.Status == protocol.RoomPlaying {
			m.ActiveGames++
		}
	}
	return m
}

// --------------------------------------------------------------- outbound

// sendToPlayer implements game.SendFunc: fetch the live conn under the lock,
// then write outside it. A stale conn simply fails its send.
func (h *Hub) sendToPlayer(playerID string, data []byte) {
	h.mu.Lock()
	var c Conn
	if p := h.players[playerID]; p != nil {
		c = p.conn
	}
	h.mu.Unlock()
	if c != nil && !c.Send(data) {
		c.Close()
	}
}

// reply marshals msg and delivers it to one player.
func (h *Hub) reply(p *Player, msg any) {
	h.sendToPlayer(p.ID(), protocol.Marshal(msg))
}

// replyErr delivers an error frame to one player.
func (h *Hub) replyErr(p *Player, code, message string) {
	h.reply(p, protocol.NewError(code, message))
}

// lobbyConnsLocked snapshots the connections of every lobby-state player —
// including waiting-room occupants, who are "in the lobby" per PROTOCOL §2.
// Caller holds h.mu.
func (h *Hub) lobbyConnsLocked() []Conn {
	var out []Conn
	for _, p := range h.players {
		if p.conn != nil && p.state != StatePlaying {
			out = append(out, p.conn)
		}
	}
	return out
}

// roomListMsgLocked builds the full room_list payload. Caller holds h.mu.
func (h *Hub) roomListMsgLocked() *protocol.RoomListMsg {
	msg := &protocol.RoomListMsg{Type: protocol.TRoomList, Rooms: []protocol.Room{}}
	for _, e := range h.rooms {
		msg.Rooms = append(msg.Rooms, protoRoomLocked(e))
	}
	return msg
}

// broadcastRoomList pushes the full list to all lobby players (PROTOCOL §2).
func (h *Hub) broadcastRoomList() {
	h.mu.Lock()
	msg := h.roomListMsgLocked()
	conns := h.lobbyConnsLocked()
	h.mu.Unlock()
	data := protocol.Marshal(msg)
	for _, c := range conns {
		if !c.Send(data) {
			c.Close()
		}
	}
}

// protoRoomLocked renders the wire Room for a room entry. Caller holds h.mu.
func protoRoomLocked(e *roomEntry) protocol.Room {
	players := make([]protocol.RoomPlayer, 0, 2)
	colors := [2]string{protocol.ColorBlack, protocol.ColorWhite}
	for i, p := range e.seats {
		if p == nil {
			continue
		}
		players = append(players, protocol.RoomPlayer{
			PlayerID:  p.ID(),
			Nick:      p.Nick(),
			Color:     protocol.StrPtr(colors[i]),
			Connected: p.conn != nil,
		})
	}
	return protocol.Room{
		ID:        e.meta.ID,
		Name:      e.meta.Name,
		Status:    e.meta.Status,
		Players:   players,
		IsAI:      e.meta.IsAI,
		CreatedAt: e.meta.CreatedAt,
	}
}

// dequeueLocked removes p from the FIFO queue if present. Caller holds h.mu.
func (h *Hub) dequeueLocked(p *Player) {
	if !p.queued {
		return
	}
	p.queued = false
	for i, q := range h.queue {
		if q == p {
			h.queue = append(h.queue[:i], h.queue[i+1:]...)
			break
		}
	}
}

// queuePositionLocked returns p's 1-based queue position (0 if not queued).
func (h *Hub) queuePositionLocked(p *Player) int {
	for i, q := range h.queue {
		if q == p {
			return i + 1
		}
	}
	return 0
}

// randID returns "prefix_" + base64url(n random bytes).
func randID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("hub: crypto/rand unavailable: " + err.Error())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b)
}
