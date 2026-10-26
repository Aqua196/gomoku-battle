package hub

import (
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/ai"
	"github.com/Aqua196/gomoku-battle/backend/internal/cluster"
	"github.com/Aqua196/gomoku-battle/backend/internal/game"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
)

// The owner side: games this node runs. Commands arrive as envelopes —
// from local sessions inline, from other nodes over the bus — and are
// turned into actor posts. With leases enabled the owner also keeps each
// game's snapshot current and its lease renewed, and adopts games whose
// owner is gone (DESIGN §10).

// roomEntry is a game owned by this node.
type roomEntry struct {
	info      cluster.RoomInfo // mirrored to the directory on every change
	actor     *game.Actor
	ai        string // AI difficulty, "" for a human game
	startedAt time.Time
	watchers  map[string]struct{}
	// epochs holds each human seat's newest socket epoch: a disconnect
	// from an older socket (raced by a resume elsewhere) is ignored.
	epochs map[string]int64
}

// startGame registers e, creates its actor (fresh, or restored from
// snapshot g), publishes the room and starts play. Seats marked offline
// start their reconnect windows.
func (h *Hub) startGame(e *roomEntry, g *cluster.LiveGame) {
	if e.watchers == nil {
		e.watchers = make(map[string]struct{})
	}
	e.epochs = make(map[string]int64)
	seats := [2]game.Seat{}
	var aiID, aiColor string
	for i, s := range e.info.Seats {
		seats[i] = game.Seat{PlayerID: s.PlayerID, Nick: s.Nick, Connected: s.Connected || s.IsAI, IsAI: s.IsAI}
		if s.IsAI {
			aiID, aiColor = s.PlayerID, [2]string{protocol.ColorBlack, protocol.ColorWhite}[i]
		}
	}
	if g == nil {
		e.startedAt = time.Now()
		e.actor = game.New(e.info.ID, seats[0], seats[1], h.cfg, h.deliver, h.gameOver)
	} else {
		e.startedAt = time.UnixMilli(g.StartedAt)
		moves := make([]protocol.Pos, len(g.Moves))
		for i, m := range g.Moves {
			moves[i] = protocol.Pos{X: m[0], Y: m[1]}
		}
		e.actor = game.Restore(e.info.ID, seats[0], seats[1], moves, e.startedAt, h.cfg, h.deliver, h.gameOver)
	}
	e.actor.SetMoveHook(h.noteMove)
	if aiID != "" {
		e.actor.AddObserver(ai.NewDriver(e.actor, e.actor.Done(), aiID, aiColor, e.ai, h.cfg.AIThink).Notify)
	}
	if h.cfg.LeaseTTL > 0 {
		e.actor.AddObserver(h.snapshotOf(e))
		if !h.dir.AcquireLease(e.info.ID, h.node, h.cfg.LeaseTTL) {
			slog.Warn("hub: lease for a new game is held elsewhere", "roomId", e.info.ID)
		}
	}

	h.mu.Lock()
	h.rooms[e.info.ID] = e
	h.mu.Unlock()
	h.dir.PutRoom(e.info)
	h.roomsChanged()
	e.actor.Start()
	if g == nil {
		for _, s := range seats {
			if !s.Connected {
				e.actor.Disconnect(s.PlayerID)
			}
		}
	}
}

// snapshotOf returns the observer that queues e's snapshot after every
// state change while the game is on.
func (h *Hub) snapshotOf(e *roomEntry) func(*protocol.GameState) {
	return func(gs *protocol.GameState) {
		if gs.Status != protocol.RoomPlaying {
			return
		}
		g := cluster.LiveGame{
			RoomID:       e.info.ID,
			Name:         e.info.Name,
			CreatedAt:    e.info.CreatedAt,
			StartedAt:    e.startedAt.UnixMilli(),
			AIDifficulty: e.ai,
			Moves:        make([][2]int, len(gs.Moves)),
		}
		for i, s := range e.info.Seats {
			g.Seats[i] = cluster.SeatInfo{PlayerID: s.PlayerID, Nick: s.Nick, IsAI: s.IsAI}
		}
		for i, m := range gs.Moves {
			g.Moves[i] = [2]int{m.X, m.Y}
		}
		h.snaps.save(g)
	}
}

// onOwnerCommand applies a game command for a room this node owns. A
// command for a room that moved (its owner died and another node restored
// it) is forwarded once to the current owner.
func (h *Hub) onOwnerCommand(c cluster.Envelope) {
	h.mu.Lock()
	e := h.rooms[c.Room]
	h.mu.Unlock()
	if e == nil {
		h.lostCommand(c)
		return
	}
	switch c.Kind {
	case cluster.KindMove:
		e.actor.Move(c.Player, c.X, c.Y)
	case cluster.KindResign:
		e.actor.Resign(c.Player)
	case cluster.KindDisconnect:
		if h.presence(e, c.Player, c.Epoch, false) {
			e.actor.Disconnect(c.Player)
		}
	case cluster.KindReconnect:
		if h.presence(e, c.Player, c.Epoch, true) {
			e.actor.Reconnect(c.Player)
		}
	case cluster.KindWatch, cluster.KindUnwatch:
		h.mu.Lock()
		if c.Kind == cluster.KindWatch {
			e.watchers[c.Player] = struct{}{}
		} else {
			delete(e.watchers, c.Player)
		}
		e.info.Spectators = len(e.watchers)
		info := e.info.Clone()
		h.mu.Unlock()
		if c.Kind == cluster.KindWatch {
			e.actor.Watch(c.Player)
		} else {
			e.actor.Unwatch(c.Player)
		}
		h.dir.PutRoom(info)
		h.roomsChanged()
	}
}

// presence records a seat's socket coming or going and reports whether
// the actor should hear about it. A disconnect older than the newest
// connect (the player resumed meanwhile) is dropped.
func (h *Hub) presence(e *roomEntry, pid string, epoch int64, online bool) bool {
	h.mu.Lock()
	if !online && epoch < e.epochs[pid] {
		h.mu.Unlock()
		return false
	}
	if online && epoch > e.epochs[pid] {
		e.epochs[pid] = epoch
	}
	changed := false
	for _, s := range e.info.Seats {
		if s.PlayerID == pid && !s.IsAI && s.Connected != online {
			s.Connected = online
			changed = true
		}
	}
	info := e.info.Clone()
	h.mu.Unlock()
	if changed {
		h.dir.PutRoom(info)
		h.roomsChanged()
	}
	return true
}

// lostCommand handles a command for a room not owned here: forward it to
// the room's current owner (once), or tell the player the game is gone.
func (h *Hub) lostCommand(c cluster.Envelope) {
	if c.Owner == "" { // not forwarded yet
		if info, ok := h.dir.Room(c.Room); ok && info.Owner != "" && info.Owner != h.node {
			c.Owner = info.Owner
			h.bus.Send(info.Owner, c)
			return
		}
	}
	switch c.Kind {
	case cluster.KindMove, cluster.KindResign:
		h.deliver(c.Player, protocol.Marshal(protocol.NewError(protocol.ENotInRoom, "not in a game")))
		h.toPlayerNode(c.Player, cluster.Envelope{Kind: cluster.KindReleased, Player: c.Player, Room: c.Room})
	case cluster.KindWatch:
		h.deliver(c.Player, protocol.Marshal(protocol.NewError(protocol.ERoomNotFound, "room not found")))
		h.toPlayerNode(c.Player, cluster.Envelope{Kind: cluster.KindWatchEnd, Player: c.Player, Room: c.Room})
	}
}

// gameOver is the actor's OverFunc: release the seats (back to the lobby,
// wherever their sessions are), detach spectators — both already got
// game_over — drop the room, its snapshot and lease, and record the game.
// A halted game (this node handing it off) leaves all shared state for the
// next owner.
func (h *Hub) gameOver(roomID string, res game.Result) {
	h.mu.Lock()
	e := h.rooms[roomID]
	delete(h.rooms, roomID)
	var watchers []string
	if e != nil {
		for w := range e.watchers {
			watchers = append(watchers, w)
		}
	}
	h.mu.Unlock()
	if e == nil || res.Halted {
		return
	}
	slog.Info("game over", "roomId", roomID, "reason", res.Reason, "moves", len(res.Moves), "node", h.node)

	h.snaps.drop(roomID)
	for _, s := range e.info.Seats {
		if s.IsAI {
			continue
		}
		h.dir.UnbindPlayer(s.PlayerID, roomID)
		h.toPlayerNode(s.PlayerID, cluster.Envelope{Kind: cluster.KindReleased, Player: s.PlayerID, Room: roomID})
	}
	for _, w := range watchers {
		h.toPlayerNode(w, cluster.Envelope{Kind: cluster.KindWatchEnd, Player: w, Room: roomID})
	}
	h.dir.DeleteRoom(roomID)
	if h.cfg.LeaseTTL > 0 {
		h.dir.ReleaseLease(roomID, h.node)
	}
	h.gamesPlayed.Add(1)
	if res.Reason != "" {
		h.store.SaveGame(recordOf(roomID, res))
	}
	h.roomsChanged()
}

// recordOf converts the actor's report into the stored game record.
func recordOf(roomID string, res game.Result) store.GameRecord {
	seat := func(s game.Seat) store.SeatRecord {
		return store.SeatRecord{PlayerID: s.PlayerID, Nick: s.Nick, IsAI: s.IsAI}
	}
	moves := make([][2]int, len(res.Moves))
	for i, m := range res.Moves {
		moves[i] = [2]int{m.X, m.Y}
	}
	return store.GameRecord{
		ID:        roomID,
		Black:     seat(res.Black),
		White:     seat(res.White),
		Moves:     moves,
		Winner:    res.Winner.Name(),
		Reason:    res.Reason,
		StartedAt: res.StartedAt.UnixMilli(),
		EndedAt:   res.EndedAt.UnixMilli(),
	}
}

// ------------------------------------------------------- leases & recovery

// maintain runs every RecoverInterval: heartbeat, renew the leases of the
// games owned here, adopt games nobody holds, and clear queue entries and
// waiting rooms left behind by dead nodes.
func (h *Hub) maintain() {
	h.dir.Heartbeat(h.node, h.cfg.LeaseTTL)
	h.renewLeases()
	h.adoptOrphans()
	h.purgeDeadNodes()
}

func (h *Hub) renewLeases() {
	h.mu.Lock()
	owned := make(map[string]*game.Actor, len(h.rooms))
	for id, e := range h.rooms {
		owned[id] = e.actor
	}
	h.mu.Unlock()
	for id, actor := range owned {
		if h.dir.RenewLease(id, h.node, h.cfg.LeaseTTL) || h.dir.AcquireLease(id, h.node, h.cfg.LeaseTTL) {
			continue
		}
		// Another node holds it: we were cut off long enough to be
		// presumed dead and the game was restored there. Two owners must
		// not both play it on, so ours stops without a verdict.
		holder, _ := h.dir.LeaseHolder(id)
		slog.Error("hub: lost a game's lease, halting our copy", "roomId", id, "holder", holder, "node", h.node)
		actor.Halt()
	}
}

// adoptOrphans restores every live game whose lease has lapsed — its owner
// crashed, or handed it off while shutting down — onto this node.
func (h *Hub) adoptOrphans() {
	for _, g := range h.dir.LiveGames() {
		if _, held := h.dir.LeaseHolder(g.RoomID); held {
			continue
		}
		h.mu.Lock()
		_, mine := h.rooms[g.RoomID]
		h.mu.Unlock()
		if mine || !h.dir.AcquireLease(g.RoomID, h.node, h.cfg.LeaseTTL) {
			continue
		}
		// Re-read under the lease: the game may have ended in between.
		idx := slices.IndexFunc(h.dir.LiveGames(), func(l cluster.LiveGame) bool { return l.RoomID == g.RoomID })
		if idx < 0 {
			h.dir.ReleaseLease(g.RoomID, h.node)
			continue
		}
		h.restore(g)
	}
}

// restore rebuilds a game from its snapshot with every human seat offline,
// then asks each human's node to re-attach a live socket (resync).
func (h *Hub) restore(g cluster.LiveGame) {
	info := cluster.RoomInfo{
		ID:        g.RoomID,
		Name:      g.Name,
		Status:    protocol.RoomPlaying,
		IsAI:      g.AIDifficulty != "",
		CreatedAt: g.CreatedAt,
		Owner:     h.node,
	}
	for i, s := range g.Seats {
		s.Connected = s.IsAI
		info.Seats[i] = &s
	}
	slog.Info("hub: restoring game", "roomId", g.RoomID, "moves", len(g.Moves), "node", h.node)
	h.startGame(&roomEntry{info: info, ai: g.AIDifficulty}, &g)
	for _, s := range g.Seats {
		if !s.IsAI {
			h.toPlayerNode(s.PlayerID, cluster.Envelope{Kind: cluster.KindResync, Player: s.PlayerID, Room: g.RoomID, Owner: h.node})
		}
	}
}

func (h *Hub) purgeDeadNodes() {
	for _, q := range h.dir.Queue() {
		if _, online := h.dir.Location(q.PlayerID); !online && !h.dir.Alive(q.Node) {
			h.dir.Dequeue(q.PlayerID)
		}
	}
	for _, r := range h.dir.Rooms() {
		if r.Status != protocol.RoomWaiting || r.Seats[0] == nil {
			continue
		}
		if _, online := h.dir.Location(r.Seats[0].PlayerID); !online && !h.dir.Alive(r.Owner) {
			if h.dir.CloseWaiting(r.ID) {
				h.roomsChanged()
			}
		}
	}
}

// ------------------------------------------------------------- snapshots

// snapshotWriter writes live-game snapshots off the actor goroutines: the
// newest snapshot per room wins, one writer goroutine flushes them. drop
// serializes with the writer, so a deleted snapshot can never be revived
// by a write that was already in flight.
type snapshotWriter struct {
	dir     cluster.Directory
	enabled bool
	mu      sync.Mutex
	pending map[string]cluster.LiveGame
	wake    chan struct{}
	writeMu sync.Mutex
}

func newSnapshotWriter(dir cluster.Directory, enabled bool) *snapshotWriter {
	return &snapshotWriter{dir: dir, enabled: enabled, pending: make(map[string]cluster.LiveGame), wake: make(chan struct{}, 1)}
}

func (w *snapshotWriter) save(g cluster.LiveGame) {
	if !w.enabled {
		return
	}
	w.mu.Lock()
	w.pending[g.RoomID] = g
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *snapshotWriter) drop(roomID string) {
	if !w.enabled {
		return
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	w.mu.Lock()
	delete(w.pending, roomID)
	w.mu.Unlock()
	w.dir.DeleteLive(roomID)
}

func (w *snapshotWriter) run(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			w.flush()
			return
		case <-w.wake:
			w.flush()
		}
	}
}

func (w *snapshotWriter) flush() {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	w.mu.Lock()
	batch := w.pending
	w.pending = make(map[string]cluster.LiveGame)
	w.mu.Unlock()
	for _, g := range batch {
		w.dir.SaveLive(g)
	}
}
