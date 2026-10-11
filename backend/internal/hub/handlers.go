package hub

import (
	"time"
	"unicode/utf8"

	"github.com/Aqua196/gomoku-battle/backend/internal/ai"
	"github.com/Aqua196/gomoku-battle/backend/internal/game"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
)

// PlayerDisconnected is the single entry point for a lost connection
// (socket error, pong timeout, slow-consumer kick). It is a no-op when the
// closing conn is stale — i.e. the player already resumed on a newer one.
//
// Semantics per DESIGN §4: lobby disconnect => drop queue seat and waiting
// room; in-game disconnect => the actor starts the reconnect window.
func (h *Hub) PlayerDisconnected(p *Player, c Conn) {
	if p == nil {
		return
	}
	var actor *game.Actor
	var roomDeleted bool
	h.mu.Lock()
	if p.conn != c {
		h.mu.Unlock()
		return // stale conn from a superseded session
	}
	p.conn = nil
	p.disconnectedAt = time.Now()
	h.dequeueLocked(p)
	switch p.state {
	case StateWaiting:
		if e := h.rooms[p.roomID]; e != nil {
			delete(h.rooms, e.meta.ID)
			h.store.DeleteRoom(e.meta.ID)
		}
		p.roomID = ""
		p.state = StateLobby
		roomDeleted = true
	case StatePlaying:
		if e := h.rooms[p.roomID]; e != nil {
			actor = e.actor
		}
		// Stay StatePlaying: the session is resumable until the window or
		// the game ends.
	}
	h.mu.Unlock()

	if actor != nil {
		actor.Disconnect(p.ID())
	}
	if roomDeleted {
		h.broadcastRoomList()
	}
}

// Hello binds a conn to a session: a fresh player, or a resumed one when the
// token matches a disconnected session (PROTOCOL §1). Returns the bound
// player, whether this was a resume, and an error code ("" on success).
//
// It deliberately does NOT notify the game actor: the caller must deliver
// welcome first and only then call ResumeGame, so the client's frame order
// is welcome -> game_state (PROTOCOL §1).
func (h *Hub) Hello(c Conn, nick, token string) (*Player, bool) {
	var p *Player
	resumed := false

	var stale Conn
	h.mu.Lock()
	if token != "" {
		if rec, ok := h.store.PlayerByToken(token); ok {
			p = h.players[rec.PlayerID]
			if p == nil {
				// Session record outlived its runtime entry; recreate it.
				p = &Player{rec: rec, state: StateLobby}
				h.players[rec.PlayerID] = p
			}
			if p.conn != nil {
				// Takeover (PROTOCOL §1): the token is the credential, so a
				// new connection with it wins — kill the old one and bind.
				stale = p.conn
			}
			p.conn = c
			p.disconnectedAt = time.Time{}
			if rec.Nick != nick {
				h.store.UpdateNick(rec.PlayerID, nick)
			}
			// Sanitize a stale room reference: e.g. the game ended while
			// the player was offline, or a waiting room was deleted.
			if p.state != StateLobby && h.rooms[p.roomID] == nil {
				p.state, p.roomID = StateLobby, ""
			}
			resumed = true
		}
		// Unknown token: fall through and issue a fresh session. A stale
		// token is not an error for the client.
	}
	if p == nil {
		rec := h.store.CreatePlayer(nick)
		p = &Player{rec: rec, conn: c, state: StateLobby}
		h.players[rec.PlayerID] = p
	}
	h.mu.Unlock()
	if stale != nil {
		stale.Close() // kicked conn's PlayerDisconnected sees p.conn!=it and no-ops
	}
	return p, resumed
}

// ResumeGame posts a reconnect event to the player's live game actor, if
// any. Call after welcome has been sent.
func (h *Hub) ResumeGame(p *Player) {
	h.mu.Lock()
	var actor *game.Actor
	if p.state == StatePlaying {
		if e := h.rooms[p.roomID]; e != nil {
			actor = e.actor
		}
	}
	h.mu.Unlock()
	if actor != nil {
		actor.Reconnect(p.ID())
	}
}

// CreateRoom seats the caller as black in a new waiting room.
func (h *Hub) CreateRoom(p *Player, name string) {
	if utf8.RuneCountInString(name) > 64 {
		h.replyErr(p, protocol.EBadRequest, "room name exceeds 64 characters")
		return
	}
	if name == "" {
		name = p.Nick() + "'s room"
	}
	var e *roomEntry
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "leave your current room first")
		return
	}
	dequeued := h.dequeueLocked(p)
	id := randID("r", 8)
	e = &roomEntry{meta: store.RoomRecord{
		ID:        id,
		Name:      name,
		Status:    protocol.RoomWaiting,
		CreatedAt: protocol.NowMs(),
	}}
	e.seats[0] = p
	h.rooms[id] = e
	h.store.UpsertRoom(e.meta)
	p.state = StateWaiting
	p.roomID = id
	room := protoRoomLocked(e)
	h.mu.Unlock()

	if dequeued {
		h.reply(p, protocol.NewMatchmaking(protocol.MatchCancelled, 0))
	}
	h.reply(p, protocol.NewRoomAction(protocol.RoomCreated, &room))
	h.broadcastRoomList()
}

// JoinRoom seats the caller as white and starts the game when full.
func (h *Hub) JoinRoom(p *Player, roomID string) {
	var e *roomEntry
	var actor *game.Actor
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "leave your current room first")
		return
	}
	e = h.rooms[roomID]
	if e == nil {
		h.mu.Unlock()
		h.replyErr(p, protocol.ERoomNotFound, "room not found")
		return
	}
	if e.meta.Status != protocol.RoomWaiting || e.seats[1] != nil {
		h.mu.Unlock()
		h.replyErr(p, protocol.ERoomFull, "room is full")
		return
	}
	dequeued := h.dequeueLocked(p)
	e.seats[1] = p
	p.roomID = roomID
	actor = h.startGameLocked(e)
	room := protoRoomLocked(e)
	h.mu.Unlock()

	if dequeued {
		h.reply(p, protocol.NewMatchmaking(protocol.MatchCancelled, 0))
	}
	for _, s := range e.seats {
		if s != nil {
			h.reply(s, protocol.NewRoomAction(protocol.RoomJoined, &room))
		}
	}
	h.broadcastRoomList()
	actor.Start()
}

// LeaveRoom exits a waiting room (deleting it) or, while playing, resigns
// the game (PROTOCOL §2, DECISIONS D3).
func (h *Hub) LeaveRoom(p *Player) {
	h.mu.Lock()
	if p.roomID == "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.ENotInRoom, "not in a room")
		return
	}
	e := h.rooms[p.roomID]
	roomID := e.meta.ID
	if e.meta.Status == protocol.RoomWaiting {
		delete(h.rooms, roomID)
		h.store.DeleteRoom(roomID)
		p.roomID = ""
		p.state = StateLobby
		h.mu.Unlock()
		h.reply(p, protocol.NewRoomLeft(roomID))
		h.broadcastRoomList()
		return
	}
	actor := e.actor
	h.mu.Unlock()

	// Playing leave = resign (D3); game_over carries the news (R12).
	if actor != nil {
		actor.Resign(p.ID())
	}
}

// ListRooms answers with the full room snapshot.
func (h *Hub) ListRooms(p *Player) {
	h.mu.Lock()
	msg := h.roomListMsgLocked()
	h.mu.Unlock()
	h.reply(p, msg)
}

// Matchmake appends the caller to the FIFO queue; reaching the front pairs
// them against the head and starts a game (PROTOCOL §2).
func (h *Hub) Matchmake(p *Player) {
	var actor *game.Actor
	position := 0
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "finish your current game first")
		return
	}
	if !p.queued {
		p.queued = true
		h.queue = append(h.queue, p)
	}
	position = h.queuePositionLocked(p)

	if len(h.queue) >= 2 {
		a, b := h.queue[0], h.queue[1]
		h.queue = h.queue[2:]
		a.queued, b.queued = false, false
		e := h.newRoomLocked(a.Nick()+"'s room", false)
		e.seats[0], e.seats[1] = a, b
		a.roomID, b.roomID = e.meta.ID, e.meta.ID
		actor = h.startGameLocked(e)
	}
	h.mu.Unlock()

	if actor != nil {
		// Paired: game_start alone carries the news; no stale queued reply.
		h.broadcastRoomList()
		actor.Start()
		return
	}
	h.reply(p, protocol.NewMatchmaking(protocol.MatchQueued, position))
}

// MatchmakeCancel removes the caller from the queue; idempotent.
func (h *Hub) MatchmakeCancel(p *Player) {
	h.mu.Lock()
	h.dequeueLocked(p)
	h.mu.Unlock()
	h.reply(p, protocol.NewMatchmaking(protocol.MatchCancelled, 0))
}

// PlayAI seats the caller as black against an embedded AI at the given
// difficulty, in an isAI room (PROTOCOL §2). The AI is a pseudo-player: it
// holds no conn and posts its moves through the same actor mailbox.
func (h *Hub) PlayAI(p *Player, difficulty string) {
	switch difficulty {
	case "easy", "normal", "hard":
	default:
		h.replyErr(p, protocol.EInvalidDifficulty, "difficulty must be easy|normal|hard")
		return
	}
	var actor *game.Actor
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "finish your current game first")
		return
	}
	dequeued := h.dequeueLocked(p)
	e := h.newRoomLocked(p.Nick()+" vs AI ("+difficulty+")", true)
	e.seats[0] = p
	e.seats[1] = &Player{
		rec:  &store.PlayerRecord{PlayerID: randID("ai", 8), Nick: "AI (" + difficulty + ")"},
		isAI: true,
	}
	p.roomID = e.meta.ID
	actor = h.startGameLocked(e)
	actor.SetObserver(ai.NewDriver(actor, actor.Done(), e.seats[1].ID(),
		protocol.ColorWhite, difficulty, h.cfg.AIThink).Notify)
	h.mu.Unlock()

	if dequeued {
		h.reply(p, protocol.NewMatchmaking(protocol.MatchCancelled, 0))
	}
	h.broadcastRoomList()
	actor.Start()
}

// Resign forfeits the caller's live game.
func (h *Hub) Resign(p *Player) {
	actor := h.gameActor(p)
	if actor == nil {
		h.replyErr(p, protocol.ENotInRoom, "not in a game")
		return
	}
	actor.Resign(p.ID())
}

// Move attempts a stone placement in the caller's live game.
func (h *Hub) Move(p *Player, x, y int) {
	actor := h.gameActor(p)
	if actor == nil {
		h.replyErr(p, protocol.ENotInRoom, "not in a game")
		return
	}
	actor.Move(p.ID(), x, y)
}

// AppPing answers an application-level ping (PROTOCOL §1).
func (h *Hub) AppPing(p *Player, ts *int64) {
	v := protocol.NowMs()
	if ts != nil {
		v = *ts
	}
	h.reply(p, &protocol.PongMsg{Type: protocol.TPong, TS: v})
}

// ------------------------------------------------------------- internals

// gameActor returns the actor for the player's current game, or nil.
func (h *Hub) gameActor(p *Player) *game.Actor {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.roomID == "" || p.state != StatePlaying {
		return nil
	}
	if e := h.rooms[p.roomID]; e != nil {
		return e.actor
	}
	return nil
}

// newRoomLocked inserts a fresh room entry. Caller holds h.mu.
func (h *Hub) newRoomLocked(name string, isAI bool) *roomEntry {
	e := &roomEntry{meta: store.RoomRecord{
		ID:        randID("r", 8),
		Name:      name,
		Status:    protocol.RoomWaiting,
		IsAI:      isAI,
		CreatedAt: protocol.NowMs(),
	}}
	h.rooms[e.meta.ID] = e
	h.store.UpsertRoom(e.meta)
	return e
}

// startGameLocked flips a room to playing, seats its players and creates the
// actor. The caller starts it after the lock is released. Caller holds h.mu.
func (h *Hub) startGameLocked(e *roomEntry) *game.Actor {
	e.meta.Status = protocol.RoomPlaying
	h.store.UpsertRoom(e.meta)
	for _, p := range e.seats {
		if p != nil {
			p.state = StatePlaying
		}
	}
	seatOf := func(i int) game.Seat {
		p := e.seats[i]
		return game.Seat{PlayerID: p.ID(), Nick: p.Nick(), Connected: true, IsAI: p.isAI}
	}
	e.actor = game.New(e.meta.ID, seatOf(0), seatOf(1), h.cfg, h.sendToPlayer, h.gameOver)
	return e.actor
}

// gameOver is the actor's OverFunc: delete the room, return both players to
// the lobby and push the refreshed room_list.
func (h *Hub) gameOver(roomID string) {
	h.mu.Lock()
	e := h.rooms[roomID]
	if e == nil {
		h.mu.Unlock()
		return
	}
	for _, p := range e.seats {
		if p != nil {
			p.state = StateLobby
			p.roomID = ""
		}
	}
	delete(h.rooms, roomID)
	h.store.DeleteRoom(roomID)
	h.gamesPlayed.Add(1)
	h.mu.Unlock()
	h.broadcastRoomList()
}
