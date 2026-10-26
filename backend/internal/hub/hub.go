// Package hub runs one node of the game server (DESIGN §2/§3/§10).
//
// A node plays two roles. As the connection side (session.go) it owns the
// sessions of the players whose sockets it holds: hello/resume, lobby
// requests, and forwarding their game commands. As the owner side
// (owner.go) it runs the rooms it owns: one game.Actor each, the seats,
// the spectators, and the game's snapshot and lease. A player and their
// room may live on different nodes; cluster.Directory says where things
// are and cluster.Bus carries frames and commands between nodes. With one
// node and the in-memory directory and bus (New), everything is local and
// behaves like the original single-process hub.
//
// Locking: h.mu guards the node's maps and session/room fields. It is never
// held across Directory or Bus calls, network writes, or actor posts, so
// there is no lock ordering with any of them.
package hub

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/cluster"
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

// Player is a session on this node, bound to a store.PlayerRecord.
type Player struct {
	rec   *store.PlayerRecord
	conn  Conn
	epoch int64 // this socket's generation (cluster.Directory.SetLocation)
	state PlayerState
	// roomID is the room the player sits in and owner the node running it.
	roomID string
	owner  string
	queued bool
	// watching is the room this lobby player spectates, on node watchOwner.
	watching   string
	watchOwner string
	// disconnectedAt marks when the conn was lost (zero while connected);
	// drives the session TTL sweep for non-playing sessions (REVIEW R4).
	disconnectedAt time.Time
}

// ID returns the player id.
func (p *Player) ID() string { return p.rec.PlayerID }

// Nick returns the current display nick.
func (p *Player) Nick() string { return p.rec.Nick }

// Token returns the resume token.
func (p *Player) Token() string { return p.rec.Token }

// Metrics is the /metrics JSON payload (PROTOCOL §7). Rooms and
// ActiveGames are cluster-wide; the rest count this node.
type Metrics struct {
	Node         string  `json:"node"`
	Connections  int     `json:"connections"`
	LobbyPlayers int     `json:"lobbyPlayers"`
	Rooms        int     `json:"rooms"`
	ActiveGames  int     `json:"activeGames"`
	OwnedGames   int     `json:"ownedGames"`
	GamesPlayed  int     `json:"gamesPlayed"`
	TotalMoves   int     `json:"totalMoves"`
	MovesPerSec  float64 `json:"movesPerSec"` // accepted moves, trailing 60s
	AiGames      int     `json:"aiGames"`
}

// Options places a hub in a cluster.
type Options struct {
	Node string // unique per process
	Dir  cluster.Directory
	Bus  cluster.Bus
}

// Hub is one node.
type Hub struct {
	cfg   protocol.Config
	store store.Store
	node  string
	dir   cluster.Directory
	bus   cluster.Bus

	mu             sync.Mutex
	players        map[string]*Player // sessions on this node, by player id
	playersByToken map[string]*Player // same sessions, by resume token
	rooms          map[string]*roomEntry
	rlDirty        bool // pending lobby room_list push (coalesced flush)

	connCount   atomic.Int64
	gamesPlayed atomic.Int64
	totalMoves  atomic.Int64
	aiGames     atomic.Int64
	mtx         sync.Mutex
	moveTimes   []int64 // unixnano of accepted moves, pruned to 60s

	// locs caches where remote players' sockets live, so delivering a
	// frame from a game actor does not wait on a Directory read. A stale
	// entry costs one hop: the wrong node forwards the frame (deliver).
	locMu sync.Mutex
	locs  map[string]cachedLoc

	snaps    *snapshotWriter
	stop     chan struct{}
	stopOnce sync.Once
	loops    sync.WaitGroup
}

// New builds a single in-memory node: the original one-process server.
func New(st store.Store, cfg protocol.Config) *Hub {
	node := randID("n", 6)
	return NewNode(st, cfg, Options{
		Node: node,
		Dir:  cluster.NewMemDirectory(),
		Bus:  cluster.NewMemNetwork().Join(node),
	})
}

// NewNode builds a node of a cluster and starts its background loops.
func NewNode(st store.Store, cfg protocol.Config, o Options) *Hub {
	h := &Hub{
		cfg:            cfg,
		store:          st,
		node:           o.Node,
		dir:            o.Dir,
		bus:            o.Bus,
		players:        make(map[string]*Player),
		playersByToken: make(map[string]*Player),
		rooms:          make(map[string]*roomEntry),
		locs:           make(map[string]cachedLoc),
		stop:           make(chan struct{}),
	}
	h.snaps = newSnapshotWriter(h.dir, cfg.LeaseTTL > 0)
	h.bus.Start(h.onEnvelope)
	h.every(cfg.SweepInterval, h.sweep)
	h.every(cfg.RoomListFlush, h.flushRoomList)
	if cfg.LeaseTTL > 0 {
		h.loops.Add(1)
		go func() {
			defer h.loops.Done()
			h.snaps.run(h.stop)
		}()
		h.maintain() // heartbeat and adopt orphaned games right away (restart)
		h.every(cfg.RecoverInterval, h.maintain)
	}
	slog.Info("hub node up", "node", h.node, "leases", cfg.LeaseTTL > 0)
	return h
}

// Node is this node's id.
func (h *Hub) Node() string { return h.node }

// every runs fn every d until Stop; d <= 0 disables it.
func (h *Hub) every(d time.Duration, fn func()) {
	if d <= 0 {
		return
	}
	h.loops.Add(1)
	go func() {
		defer h.loops.Done()
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-h.stop:
				return
			case <-t.C:
				fn()
			}
		}
	}()
}

// Stop halts the background loops. Idempotent.
func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
		h.loops.Wait()
	})
}

// Handoff prepares a graceful exit (DESIGN §10): halt every game this node
// owns without a verdict, write their final snapshots and release their
// leases, so another node — or this server restarted — restores them
// within one recovery interval instead of waiting out the lease. Call it
// before closing the sockets.
func (h *Hub) Handoff() {
	h.mu.Lock()
	actors := make([]*game.Actor, 0, len(h.rooms))
	ids := make([]string, 0, len(h.rooms))
	for id, e := range h.rooms {
		actors = append(actors, e.actor)
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, a := range actors {
		a.Halt()
		<-a.Done()
	}
	h.snaps.flush()
	for _, id := range ids {
		h.dir.ReleaseLease(id, h.node)
	}
	slog.Info("hub handed off its games", "node", h.node, "games", len(actors))
}

// Kill simulates a crash, for tests and failover drills: games stop with
// no verdict and no cleanup, the loops stop, the node leaves the bus.
// Leases expire and other nodes adopt the games, as if the process died.
func (h *Hub) Kill() {
	h.mu.Lock()
	actors := make([]*game.Actor, 0, len(h.rooms))
	for _, e := range h.rooms {
		actors = append(actors, e.actor)
	}
	h.mu.Unlock()
	for _, a := range actors {
		a.Halt()
		<-a.Done()
	}
	h.Close()
}

// Close stops the node and leaves the bus. Idempotent.
func (h *Hub) Close() {
	h.Stop()
	h.bus.Close()
}

// sweep expires disconnected non-playing sessions on SessionTTL. Playing
// sessions are never reaped here: their fate belongs to the game actor (the
// reconnect window and the double-disconnect abandon rules come first).
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
	rooms := h.dir.Rooms()
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
		Node:        h.node,
		Connections: int(h.connCount.Load()),
		Rooms:       len(rooms),
		GamesPlayed: int(h.gamesPlayed.Load()),
		TotalMoves:  int(h.totalMoves.Load()),
		MovesPerSec: float64(recent) / 60,
		AiGames:     int(h.aiGames.Load()),
	}
	for _, r := range rooms {
		if r.Status == protocol.RoomPlaying {
			m.ActiveGames++
		}
	}
	h.mu.Lock()
	for _, p := range h.players {
		if p.conn != nil && p.state != StatePlaying {
			m.LobbyPlayers++
		}
	}
	m.OwnedGames = len(h.rooms)
	h.mu.Unlock()
	return m
}

// --------------------------------------------------------------- outbound

// localConn returns the socket of a session on this node (nil while it is
// disconnected) and whether such a session exists.
func (h *Hub) localConn(pid string) (Conn, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.players[pid]
	if p == nil {
		return nil, false
	}
	return p.conn, true
}

// deliver implements game.SendFunc: write to the player's socket here, or
// forward the frame to the node holding it. A player with a session here
// but no socket is offline; the frame is dropped (they resync on resume).
func (h *Hub) deliver(pid string, data []byte) {
	if c, here := h.localConn(pid); here {
		if c != nil && !c.Send(data) {
			c.Close()
		}
		return
	}
	if node, ok := h.locate(pid); ok && node != h.node {
		h.bus.Send(node, cluster.Envelope{Kind: cluster.KindDeliver, Player: pid, Frame: data})
	}
}

type cachedLoc struct {
	node    string
	expires time.Time
}

// locTTL bounds how long a cached location is trusted without a re-read.
const locTTL = 5 * time.Second

// locate finds the node holding pid's socket: cache first, then the
// Directory.
func (h *Hub) locate(pid string) (string, bool) {
	now := time.Now()
	h.locMu.Lock()
	l, ok := h.locs[pid]
	h.locMu.Unlock()
	if ok && now.Before(l.expires) {
		return l.node, true
	}
	node, found := h.dir.Location(pid)
	h.locMu.Lock()
	if found {
		h.locs[pid] = cachedLoc{node: node, expires: now.Add(locTTL)}
	} else {
		delete(h.locs, pid)
	}
	if len(h.locs) > 1<<16 { // bound memory: an occasional full flush is cheap
		clear(h.locs)
	}
	h.locMu.Unlock()
	return node, found
}

// reply marshals msg and delivers it to a session.
func (h *Hub) reply(p *Player, msg any) {
	h.deliver(p.ID(), protocol.Marshal(msg))
}

// replyErr delivers an error frame to a session.
func (h *Hub) replyErr(p *Player, code, message string) {
	h.reply(p, protocol.NewError(code, message))
}

// toNode runs e on node: inline when that is this node, else over the bus.
func (h *Hub) toNode(node string, e cluster.Envelope) {
	if node == "" {
		return
	}
	if node == h.node {
		e.From = h.node
		h.onEnvelope(e)
		return
	}
	h.bus.Send(node, e)
}

// toPlayerNode runs e where pid's socket lives (if anywhere).
func (h *Hub) toPlayerNode(pid string, e cluster.Envelope) {
	if _, here := h.localConn(pid); here {
		h.toNode(h.node, e)
		return
	}
	if node, ok := h.locate(pid); ok {
		h.toNode(node, e)
	}
}

// onEnvelope dispatches an inbound bus message (or a local short-cut).
func (h *Hub) onEnvelope(e cluster.Envelope) {
	switch e.Kind {
	case cluster.KindDeliver:
		c, here := h.localConn(e.Player)
		if !here {
			// The sender's cached location was stale: pass the frame on
			// once to wherever the player is now.
			if node, ok := h.dir.Location(e.Player); ok && node != h.node && e.Owner == "" {
				e.Owner = node
				h.bus.Send(node, e)
			}
			return
		}
		if c != nil && !c.Send(e.Frame) {
			c.Close()
		}
	case cluster.KindLobby:
		h.markRoomListDirty()
	case cluster.KindSeated, cluster.KindReleased, cluster.KindWatchEnd, cluster.KindKick, cluster.KindResync:
		h.onSessionNotice(e)
	default:
		h.onOwnerCommand(e)
	}
}

// ------------------------------------------------------------- room list

// roomsChanged tells every node (this one included) to refresh the lobby.
func (h *Hub) roomsChanged() {
	h.bus.Broadcast(cluster.Envelope{Kind: cluster.KindLobby})
	h.markRoomListDirty()
}

// markRoomListDirty schedules a lobby push: coalesced by the flush loop
// when RoomListFlush > 0, else pushed right away (the synchronous path
// tests rely on).
func (h *Hub) markRoomListDirty() {
	if h.cfg.RoomListFlush > 0 {
		h.mu.Lock()
		h.rlDirty = true // only the flush loop consumes this flag (R10)
		h.mu.Unlock()
		return
	}
	h.pushRoomList()
}

func (h *Hub) flushRoomList() {
	h.mu.Lock()
	dirty := h.rlDirty
	h.rlDirty = false
	h.mu.Unlock()
	if dirty {
		h.pushRoomList()
	}
}

// pushRoomList renders the cluster's room list once and pushes it to every
// lobby session on this node — including waiting-room occupants, who are
// "in the lobby" per PROTOCOL §2.
func (h *Hub) pushRoomList() {
	h.mu.Lock()
	conns := make([]Conn, 0, len(h.players))
	for _, p := range h.players {
		if p.conn != nil && p.state != StatePlaying {
			conns = append(conns, p.conn)
		}
	}
	h.mu.Unlock()
	if len(conns) == 0 {
		return // nobody in this node's lobby: skip the directory read
	}
	data := protocol.Marshal(h.roomListMsg())
	for _, c := range conns {
		if !c.SendRoomList(data) {
			c.Close()
		}
	}
}

// roomListMsg builds the full room_list payload from the directory.
func (h *Hub) roomListMsg() *protocol.RoomListMsg {
	msg := &protocol.RoomListMsg{Type: protocol.TRoomList, Rooms: []protocol.Room{}}
	for _, r := range h.dir.Rooms() {
		msg.Rooms = append(msg.Rooms, wireRoom(r))
	}
	return msg
}

// wireRoom renders a directory room as the lobby-facing Room.
func wireRoom(r cluster.RoomInfo) protocol.Room {
	players := make([]protocol.RoomPlayer, 0, 2)
	colors := [2]string{protocol.ColorBlack, protocol.ColorWhite}
	for i, s := range r.Seats {
		if s == nil {
			continue
		}
		players = append(players, protocol.RoomPlayer{
			PlayerID:  s.PlayerID,
			Nick:      s.Nick,
			Color:     protocol.StrPtr(colors[i]),
			Connected: s.Connected || s.IsAI,
		})
	}
	return protocol.Room{
		ID:         r.ID,
		Name:       r.Name,
		Status:     r.Status,
		Players:    players,
		IsAI:       r.IsAI,
		CreatedAt:  r.CreatedAt,
		Spectators: r.Spectators,
	}
}

// randSeat flips a fair coin for play_ai{color:"random"}: 0 black, 1 white.
func randSeat() int {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("hub: crypto/rand unavailable: " + err.Error())
	}
	return int(b[0] & 1)
}

// randID returns "prefix_" + base64url(n random bytes).
func randID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("hub: crypto/rand unavailable: " + err.Error())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b)
}
