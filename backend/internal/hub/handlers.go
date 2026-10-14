package hub

import (
	"log/slog"
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
	var deletedRoomID string
	h.mu.Lock()
	if p.conn != c {
		h.mu.Unlock()
		return // stale conn from a superseded session
	}
	p.conn = nil
	p.disconnectedAt = time.Now()
	h.dequeueLocked(p)
	slog.Info("player disconnected", "playerId", p.ID(), "roomId", p.roomID)
	switch p.state {
	case StateWaiting:
		if e := h.rooms[p.roomID]; e != nil {
			deletedRoomID = e.meta.ID
			delete(h.rooms, deletedRoomID)
		}
		p.roomID = ""
		p.state = StateLobby
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
	if deletedRoomID != "" {
		h.syncRoomStore(deletedRoomID)
		h.broadcastRoomList()
	}
}

// bindResumedLocked attaches conn c to an existing runtime Player session
// (handling takeover if a previous conn is still present) and returns the
// displaced conn (if any) plus whether the display nick changed. Caller
// holds h.mu.
func (h *Hub) bindResumedLocked(p *Player, c Conn, nick string) (stale Conn, nickChanged bool) {
	if p.conn != nil {
		// Takeover (PROTOCOL §1): the token is the credential, so a
		// new connection with it wins — kill the old one and bind.
		stale = p.conn
	}
	p.conn = c
	p.disconnectedAt = time.Time{}
	if p.rec.Nick != nick {
		p.rec.Nick = nick
		nickChanged = true
	}
	// Sanitize a stale room reference: e.g. the game ended while
	// the player was offline, or a waiting room was deleted.
	if p.state != StateLobby && h.rooms[p.roomID] == nil {
		p.state, p.roomID = StateLobby, ""
	}
	slog.Info("player resumed", "playerId", p.ID(), "roomId", p.roomID)
	return stale, nickChanged
}

// Hello binds a conn to a session: a fresh player, or a resumed one when the
// token matches a disconnected session (PROTOCOL §1). Returns the bound
// player and whether this was a resume.
//
// All Store I/O runs outside h.mu so a slow or unreachable Redis can never
// freeze the hub, while the authoritative in-memory playersByToken index is
// consulted first so transient Redis faults never drop active sessions.
func (h *Hub) Hello(c Conn, nick, token string) (*Player, bool) {
	if token != "" {
		h.mu.Lock()
		if p := h.playersByToken[token]; p != nil {
			stale, nickChanged := h.bindResumedLocked(p, c, nick)
			pid := p.ID()
			h.mu.Unlock()
			if stale != nil {
				stale.Close()
			}
			if nickChanged {
				h.store.UpdateNick(pid, nick)
			} else {
				// Slide the backing store's TTL outside h.mu.
				_, _ = h.store.PlayerByID(pid)
			}
			return p, true
		}
		h.mu.Unlock()

		// Cross-restart fallback: consult the persistent store outside h.mu.
		if rec, ok := h.store.PlayerByToken(token); ok {
			h.mu.Lock()
			p := h.players[rec.PlayerID]
			if p == nil {
				p = &Player{rec: rec, state: StateLobby}
				h.players[rec.PlayerID] = p
				h.playersByToken[rec.Token] = p
			}
			stale, nickChanged := h.bindResumedLocked(p, c, nick)
			pid := p.ID()
			h.mu.Unlock()
			if stale != nil {
				stale.Close()
			}
			if nickChanged {
				h.store.UpdateNick(pid, nick)
			}
			return p, true
		}
		// Unknown token: fall through and issue a fresh session. A stale
		// token is not an error for the client.
	}

	// Fresh session: allocate and persist outside h.mu, then register.
	rec := h.store.CreatePlayer(nick)
	h.mu.Lock()
	p := &Player{rec: rec, conn: c, state: StateLobby}
	h.players[rec.PlayerID] = p
	h.playersByToken[rec.Token] = p
	slog.Info("player connected", "playerId", p.ID(), "nick", p.Nick())
	h.mu.Unlock()
	return p, false
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
	p.state = StateWaiting
	p.roomID = id
	room := protoRoomLocked(e)
	h.mu.Unlock()

	h.syncRoomStore(id)
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
	slog.Info("game started", "roomId", roomID, "joinedBy", p.ID())
	h.mu.Unlock()

	h.syncRoomStore(roomID)
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
		p.roomID = ""
		p.state = StateLobby
		h.mu.Unlock()
		h.syncRoomStore(roomID)
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
	var pairedRoomID string
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
		pairedRoomID = e.meta.ID
		actor = h.startGameLocked(e)
		slog.Info("game started", "roomId", e.meta.ID, "black", a.ID(), "white", b.ID())
	}
	h.mu.Unlock()

	if actor != nil {
		// Paired: persist room outside h.mu, then broadcast and start actor.
		h.syncRoomStore(pairedRoomID)
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
	var roomID string
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
	roomID = e.meta.ID
	p.roomID = roomID
	actor = h.startGameLocked(e)
	actor.SetObserver(ai.NewDriver(actor, actor.Done(), e.seats[1].ID(),
		protocol.ColorWhite, difficulty, h.cfg.AIThink).Notify)
	h.aiGames.Add(1)
	slog.Info("ai game started", "playerId", p.ID(), "roomId", roomID, "difficulty", difficulty)
	h.mu.Unlock()

	h.syncRoomStore(roomID)
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

// newRoomLocked inserts a fresh room entry in memory. Store persistence is
// performed by the caller via syncRoomStore after releasing h.mu.
func (h *Hub) newRoomLocked(name string, isAI bool) *roomEntry {
	e := &roomEntry{meta: store.RoomRecord{
		ID:        randID("r", 8),
		Name:      name,
		Status:    protocol.RoomWaiting,
		IsAI:      isAI,
		CreatedAt: protocol.NowMs(),
	}}
	h.rooms[e.meta.ID] = e
	return e
}

// startGameLocked flips a room to playing, seats its players and creates the
// actor. The caller syncs the store and starts the actor after h.mu is released.
func (h *Hub) startGameLocked(e *roomEntry) *game.Actor {
	e.meta.Status = protocol.RoomPlaying
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
	e.actor.SetMoveHook(h.noteMove)
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
	slog.Info("game over", "roomId", roomID, "players", len(e.seats))
	for _, p := range e.seats {
		if p != nil {
			p.state = StateLobby
			p.roomID = ""
		}
	}
	delete(h.rooms, roomID)
	h.gamesPlayed.Add(1)
	h.mu.Unlock()

	h.syncRoomStore(roomID)
	h.broadcastRoomList()
}
