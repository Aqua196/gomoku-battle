// Package hub is the mutex-protected registry at the center of the server:
// players, rooms, the FIFO matchmaking queue and the lobby broadcast
// (DESIGN §2/§3). It never sends network traffic or performs Store I/O while
// holding its lock — every outbound write is a non-blocking TrySend into a
// connection's buffered channel, every Store persistence call runs outside
// h.mu, and every game-actor post happens after the lock is released, so
// there is no lock ordering with actor or network code.
package hub

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"sync/atomic"
	"time"

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
	// SendRoomList queues a room_list snapshot with merge semantics: only
	// the newest pending snapshot is kept (PROTOCOL §2 dirty-bit merge).
	SendRoomList(data []byte) bool
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
	// disconnectedAt marks when the conn was lost (zero while connected);
	// drives the session TTL sweep for non-playing sessions (REVIEW R4).
	disconnectedAt time.Time
	// isAI marks the pseudo-player seated in an isAI room: it never holds
	// a conn, never enters h.players, and renders as connected.
	isAI bool
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
	Connections  int     `json:"connections"`
	LobbyPlayers int     `json:"lobbyPlayers"`
	Rooms        int     `json:"rooms"`
	ActiveGames  int     `json:"activeGames"`
	GamesPlayed  int     `json:"gamesPlayed"`
	TotalMoves   int     `json:"totalMoves"`
	MovesPerSec  float64 `json:"movesPerSec"` // accepted moves, trailing 60s
	AiGames      int     `json:"aiGames"`
}

// Hub owns the player/room/queue registries.
type Hub struct {
	cfg   protocol.Config
	store store.Store

	// storeMu serializes room Store persistence outside h.mu (lock order:
	// storeMu -> mu, never mu -> storeMu) so concurrent UpsertRoom and
	// DeleteRoom calls never reorder while keeping network I/O out of h.mu.
	storeMu        sync.Mutex
	mu             sync.Mutex
	players        map[string]*Player // by player id; disconnected players stay resumable
	playersByToken map[string]*Player // by resume token; authoritative runtime index
	rooms          map[string]*roomEntry
	queue          []*Player // FIFO

	connCount   atomic.Int64
	gamesPlayed atomic.Int64
	totalMoves  atomic.Int64
	aiGames     atomic.Int64
	mtx         sync.Mutex
	moveTimes   []int64 // unixnano of accepted moves, pruned to 60s

	sweepStop chan struct{}
	rlDirty   bool // pending lobby room_list broadcast (coalesced flush)
}

// New builds a hub around a Store and an injected timing config and starts
// the session sweeper (R4: disconnected non-playing sessions expire).
func New(st store.Store, cfg protocol.Config) *Hub {
	h := &Hub{
		cfg:            cfg,
		store:          st,
		players:        make(map[string]*Player),
		playersByToken: make(map[string]*Player),
		rooms:          make(map[string]*roomEntry),
		sweepStop:      make(chan struct{}),
	}
	if cfg.SweepInterval > 0 {
		go h.sweepLoop()
	}
	if cfg.RoomListFlush > 0 {
		go h.roomListFlushLoop()
	}
	return h
}

// Stop halts the background sweeper.
func (h *Hub) Stop() { close(h.sweepStop) }

// syncRoomStore persists the latest authoritative state of roomID to h.store
// outside h.mu. Serializing under storeMu before briefly inspecting h.rooms
// guarantees that a rapid CreateRoom -> DeleteRoom sequence never leaves a
// stale room in the Store.
func (h *Hub) syncRoomStore(roomID string) {
	if roomID == "" {
		return
	}
	h.storeMu.Lock()
	defer h.storeMu.Unlock()

	h.mu.Lock()
	var meta store.RoomRecord
	exists := false
	if e := h.rooms[roomID]; e != nil {
		meta = e.meta
		exists = true
	}
	h.mu.Unlock()

	if exists {
		h.store.UpsertRoom(meta)
	} else {
		h.store.DeleteRoom(roomID)
	}
}

// sweepLoop expires disconnected non-playing sessions on SessionTTL. Playing
// sessions are never reaped here: their fate belongs to the game actor (the
// reconnect window and the double-disconnect abandon rules come first).
func (h *Hub) sweepLoop() {
	ticker := time.NewTicker(h.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.sweepStop:
			return
		case <-ticker.C:
			h.sweep()
		}
	}
}

func (h *Hub) sweep() {
	now := time.Now()
	var expired []string
	h.mu.Lock()
	for id, p := range h.players {
		if p.conn != nil || p.disconnectedAt.IsZero() || p.state == StatePlaying {
			continue
		}
		if now.Sub(p.disconnectedAt) > h.cfg.SessionTTL {
			delete(h.players, id)
			delete(h.playersByToken, p.Token())
			expired = append(expired, id)
		}
	}
	h.mu.Unlock()
	for _, id := range expired {
		h.store.DeletePlayer(id)
	}
}

// ConnOpened counts a freshly accepted socket for metrics.
func (h *Hub) ConnOpened() { h.connCount.Add(1) }

// ConnClosed counts a socket that finished its read loop for metrics.
func (h *Hub) ConnClosed() { h.connCount.Add(-1) }

// noteMove is the actor's move hook: bumps total and timestamps the move
// into the trailing 60s window for movesPerSec.
func (h *Hub) noteMove(_ string) {
	h.totalMoves.Add(1)
	h.mtx.Lock()
	now := time.Now().UnixNano()
	h.moveTimes = append(h.moveTimes, now)
	// prune the window on write too, so the slice stays O(60s of moves)
	// even when nobody ever hits /metrics
	cutoff := now - int64(time.Minute)
	i := 0
	for i < len(h.moveTimes) && h.moveTimes[i] < cutoff {
		i++
	}
	h.moveTimes = append(h.moveTimes[:0], h.moveTimes[i:]...)
	h.mtx.Unlock()
}

// Metrics snapshots the counters for /metrics.
func (h *Hub) Metrics() Metrics {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-time.Minute).UnixNano()
	h.mtx.Lock()
	keep := h.moveTimes[:0]
	for _, ts := range h.moveTimes {
		if ts >= cutoff {
			keep = append(keep, ts)
		}
	}
	h.moveTimes = keep
	recent := len(keep)
	h.mtx.Unlock()
	m := Metrics{
		Connections: int(h.connCount.Load()),
		Rooms:       len(h.rooms),
		GamesPlayed: int(h.gamesPlayed.Load()),
		TotalMoves:  int(h.totalMoves.Load()),
		MovesPerSec: float64(recent) / 60,
		AiGames:     int(h.aiGames.Load()),
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

// broadcastRoomList marks the lobby list dirty; RoomListFlush>0 coalesces
// bursts of mutations into one render+push per interval, else it pushes
// immediately (zero: the synchronous path tests rely on).
func (h *Hub) broadcastRoomList() {
	h.mu.Lock()
	if h.cfg.RoomListFlush > 0 {
		h.rlDirty = true // only the flush path consumes this flag (R10)
		h.mu.Unlock()
		return
	}
	msg := h.roomListMsgLocked()
	conns := h.lobbyConnsLocked()
	h.mu.Unlock()
	h.pushRoomList(msg, conns)
}

// roomListFlushLoop emits at most one lobby broadcast per RoomListFlush.
func (h *Hub) roomListFlushLoop() {
	ticker := time.NewTicker(h.cfg.RoomListFlush)
	defer ticker.Stop()
	for {
		select {
		case <-h.sweepStop:
			return
		case <-ticker.C:
			h.mu.Lock()
			if !h.rlDirty {
				h.mu.Unlock()
				continue
			}
			h.rlDirty = false
			msg := h.roomListMsgLocked()
			conns := h.lobbyConnsLocked()
			h.mu.Unlock()
			h.pushRoomList(msg, conns)
		}
	}
}

func (h *Hub) pushRoomList(msg *protocol.RoomListMsg, conns []Conn) {
	data := protocol.Marshal(msg)
	for _, c := range conns {
		if !c.SendRoomList(data) {
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
			Connected: p.conn != nil || p.isAI,
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

// dequeueLocked removes p from the FIFO queue if present and reports
// whether anything was removed. Caller holds h.mu.
func (h *Hub) dequeueLocked(p *Player) bool {
	if !p.queued {
		return false
	}
	p.queued = false
	for i, q := range h.queue {
		if q == p {
			h.queue = append(h.queue[:i], h.queue[i+1:]...)
			break
		}
	}
	return true
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
