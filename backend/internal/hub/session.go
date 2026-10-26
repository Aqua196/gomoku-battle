package hub

import (
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/Aqua196/gomoku-battle/backend/internal/cluster"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

// The connection side: requests from sockets on this node. Admission into
// rooms is decided by the directory (BindPlayer / ClaimSeat / Enqueue are
// atomic cluster-wide); the session fields only cache the outcome so game
// commands can be routed without a lookup.
//
// Waiting rooms live in the directory alone: whichever node handles the
// join (or the matchmaking pair, or play_ai) starts the game and owns it.

// watchExit is a spectator detachment still to be announced.
type watchExit struct {
	roomID, owner string
}

// unwatchLocked detaches p from the game it spectates. Caller holds h.mu;
// finish the exit with leave after unlocking.
func (h *Hub) unwatchLocked(p *Player) watchExit {
	x := watchExit{roomID: p.watching, owner: p.watchOwner}
	p.watching, p.watchOwner = "", ""
	return x
}

// leave tells the owner (and, with notify, the client via spectate_end).
// It reports whether anything was detached.
func (x watchExit) leave(h *Hub, p *Player, notify bool) bool {
	if x.roomID == "" {
		return false
	}
	h.toNode(x.owner, cluster.Envelope{Kind: cluster.KindUnwatch, Player: p.ID(), Room: x.roomID})
	if notify {
		h.reply(p, &protocol.SpectateEndMsg{Type: protocol.TSpectateEnd, RoomID: x.roomID})
	}
	return true
}

// Hello binds a socket to a session: a fresh player, or a resumed one when
// the token matches (PROTOCOL §1) — a session on this node first, else the
// persistent store (another node, or before a restart). Taking over a live
// socket closes the old one; a socket on another node is kicked there.
func (h *Hub) Hello(c Conn, nick, token string) (*Player, bool) {
	var p *Player
	if token != "" {
		h.mu.Lock()
		p = h.playersByToken[token]
		h.mu.Unlock()
		if p == nil {
			if rec, ok := h.store.PlayerByToken(token); ok {
				p = h.adopt(&Player{rec: rec})
			}
		}
	}
	resumed := p != nil
	if !resumed {
		p = h.adopt(&Player{rec: h.store.CreatePlayer(nick)})
	}

	epoch, prev := h.dir.SetLocation(p.ID(), h.node)
	h.mu.Lock()
	stale := p.conn
	p.conn, p.epoch = c, epoch
	p.disconnectedAt = time.Time{}
	nickChanged := p.rec.Nick != nick
	p.rec.Nick = nick
	pid := p.ID()
	h.mu.Unlock()

	if stale != nil && stale != c {
		stale.Close() // takeover (PROTOCOL §1): its read pump finds itself superseded
	}
	if prev != "" && prev != h.node {
		h.bus.Send(prev, cluster.Envelope{Kind: cluster.KindKick, Player: pid, Epoch: epoch})
	}
	switch {
	case nickChanged:
		h.store.UpdateNick(pid, nick)
	case resumed:
		_, _ = h.store.PlayerByID(pid) // slide the store's TTL
	}
	if resumed {
		slog.Info("player resumed", "playerId", pid, "node", h.node)
	} else {
		slog.Info("player connected", "playerId", pid, "nick", nick, "node", h.node)
	}
	return p, resumed
}

// adopt registers a session, or returns the one a concurrent hello already
// registered for the same player.
func (h *Hub) adopt(p *Player) *Player {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur := h.players[p.ID()]; cur != nil {
		return cur
	}
	h.players[p.ID()] = p
	h.playersByToken[p.Token()] = p
	return p
}

// Reseat restores a resumed session's place from the directory — a live
// game, a waiting room, or the lobby — and returns the live game's room id
// ("" for none) for the welcome frame. Call before sending welcome.
func (h *Hub) Reseat(p *Player) string {
	var info cluster.RoomInfo
	roomID, bound := h.dir.PlayerRoom(p.ID())
	if bound {
		info, bound = h.dir.Room(roomID)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case !bound:
		p.state, p.roomID, p.owner = StateLobby, "", ""
		return ""
	case info.Status == protocol.RoomWaiting:
		p.state, p.roomID, p.owner = StateWaiting, roomID, ""
		return ""
	default:
		p.state, p.roomID, p.owner = StatePlaying, roomID, info.Owner
		return roomID
	}
}

// ResumeGame asks the owner of the session's live game to re-attach it
// (the owner answers with the full game_state). Call after welcome.
func (h *Hub) ResumeGame(p *Player) {
	roomID, owner, ok := h.liveGame(p)
	if !ok {
		return
	}
	h.mu.Lock()
	epoch := p.epoch
	h.mu.Unlock()
	h.toNode(owner, cluster.Envelope{Kind: cluster.KindReconnect, Player: p.ID(), Room: roomID, Epoch: epoch})
}

// PlayerDisconnected is the single entry point for a lost connection
// (socket error, pong timeout, slow-consumer kick, shutdown). It is a no-op
// when the closing conn is stale — the player already resumed on a newer
// one, here or (after a kick) elsewhere.
//
// Semantics per DESIGN §4: lobby disconnect => drop queue seat, waiting
// room and spectating; in-game disconnect => the owner starts the
// reconnect window.
func (h *Hub) PlayerDisconnected(p *Player, c Conn) {
	if p == nil {
		return
	}
	h.mu.Lock()
	if p.conn != c {
		h.mu.Unlock()
		return
	}
	p.conn = nil
	p.disconnectedAt = time.Now()
	queued := p.queued
	p.queued = false
	watch := h.unwatchLocked(p)
	state, roomID, owner, epoch := p.state, p.roomID, p.owner, p.epoch
	pid := p.ID()
	h.mu.Unlock()
	slog.Info("player disconnected", "playerId", pid, "roomId", roomID)

	if queued {
		h.dir.Dequeue(pid)
	}
	watch.leave(h, p, false)
	switch state {
	case StateWaiting:
		h.closeWaiting(p, roomID)
	case StatePlaying:
		h.toNode(owner, cluster.Envelope{Kind: cluster.KindDisconnect, Player: pid, Room: roomID, Epoch: epoch})
	}
	h.dir.ClearLocation(pid, h.node, epoch)
	if watch.roomID != "" {
		h.roomsChanged()
	}
}

// closeWaiting removes p's waiting room unless a join has just claimed it
// (then the game is starting and p hears "seated").
func (h *Hub) closeWaiting(p *Player, roomID string) bool {
	if !h.dir.CloseWaiting(roomID) {
		return false
	}
	h.mu.Lock()
	if p.roomID == roomID {
		p.state, p.roomID, p.owner = StateLobby, "", ""
	}
	h.mu.Unlock()
	h.roomsChanged()
	return true
}

// leaveQueue removes p from matchmaking and reports whether it was queued.
// Caller holds h.mu; the directory call happens in dequeue.
func (h *Hub) takeQueuedLocked(p *Player) bool {
	q := p.queued
	p.queued = false
	return q
}

func (h *Hub) dequeue(p *Player, wasQueued bool) {
	if wasQueued && h.dir.Dequeue(p.ID()) {
		h.reply(p, protocol.NewMatchmaking(protocol.MatchCancelled, 0))
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
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "leave your current room first")
		return
	}
	queued := h.takeQueuedLocked(p)
	watch := h.unwatchLocked(p)
	nick := p.Nick()
	h.mu.Unlock()

	h.dequeue(p, queued)
	watch.leave(h, p, true)
	id := randID("r", 8)
	if !h.dir.BindPlayer(p.ID(), id) {
		h.replyErr(p, protocol.EAlreadyInRoom, "leave your current room first")
		return
	}
	info := cluster.RoomInfo{
		ID:        id,
		Name:      name,
		Status:    protocol.RoomWaiting,
		CreatedAt: protocol.NowMs(),
		Owner:     h.node,
		Seats:     [2]*cluster.SeatInfo{{PlayerID: p.ID(), Nick: nick, Connected: true}},
	}
	h.dir.PutRoom(info)
	h.mu.Lock()
	p.state, p.roomID, p.owner = StateWaiting, id, ""
	h.mu.Unlock()

	room := wireRoom(info)
	h.reply(p, protocol.NewRoomAction(protocol.RoomCreated, &room))
	h.roomsChanged()
}

// JoinRoom claims the white seat of a waiting room; this node then starts
// and owns the game.
func (h *Hub) JoinRoom(p *Player, roomID string) {
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "leave your current room first")
		return
	}
	nick := p.Nick()
	h.mu.Unlock()

	info, err := h.dir.ClaimSeat(roomID, cluster.SeatInfo{PlayerID: p.ID(), Nick: nick, Connected: true})
	switch {
	case errors.Is(err, cluster.ErrNoRoom):
		h.replyErr(p, protocol.ERoomNotFound, "room not found")
		return
	case errors.Is(err, cluster.ErrRoomFull):
		h.replyErr(p, protocol.ERoomFull, "room is full")
		return
	case err != nil:
		h.replyErr(p, protocol.EAlreadyInRoom, "leave your current room first")
		return
	}
	h.mu.Lock()
	queued := h.takeQueuedLocked(p)
	watch := h.unwatchLocked(p)
	h.mu.Unlock()
	h.dequeue(p, queued)
	watch.leave(h, p, true)

	// The creator may sit on any node, or be gone (their room closing
	// raced this claim): seat them wherever they are, offline if nowhere.
	creator := info.Seats[0]
	_, creator.Connected = h.dir.Location(creator.PlayerID)
	info.Owner = h.node
	room := wireRoom(info)
	for _, s := range info.Seats {
		h.toPlayerNode(s.PlayerID, cluster.Envelope{Kind: cluster.KindSeated, Player: s.PlayerID, Room: roomID, Owner: h.node})
		h.deliver(s.PlayerID, protocol.Marshal(protocol.NewRoomAction(protocol.RoomJoined, &room)))
	}
	slog.Info("game started", "roomId", roomID, "joinedBy", p.ID(), "node", h.node)
	h.startGame(&roomEntry{info: info}, nil)
}

// LeaveRoom exits a waiting room (deleting it) or, while playing, resigns
// the game (PROTOCOL §2, DECISIONS D3).
func (h *Hub) LeaveRoom(p *Player) {
	h.mu.Lock()
	state, roomID, owner := p.state, p.roomID, p.owner
	h.mu.Unlock()
	switch {
	case roomID == "":
		h.replyErr(p, protocol.ENotInRoom, "not in a room")
	case state == StateWaiting:
		// A join that claimed the room first wins: the game starts instead.
		if h.closeWaiting(p, roomID) {
			h.reply(p, protocol.NewRoomLeft(roomID))
		}
	default:
		// Playing leave = resign (D3); game_over carries the news (R12).
		h.toNode(owner, cluster.Envelope{Kind: cluster.KindResign, Player: p.ID(), Room: roomID})
	}
}

// ListRooms answers with the full room snapshot.
func (h *Hub) ListRooms(p *Player) {
	h.reply(p, h.roomListMsg())
}

// Matchmake joins the cluster-wide FIFO queue; when it pairs, this node
// starts and owns the game (PROTOCOL §2).
func (h *Hub) Matchmake(p *Player) {
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "finish your current game first")
		return
	}
	entry := cluster.QueueEntry{PlayerID: p.ID(), Nick: p.Nick(), Node: h.node}
	h.mu.Unlock()

	roomID := randID("r", 8)
	pair, position := h.dir.Enqueue(entry, roomID)
	if pair == nil {
		if position == 0 {
			h.replyErr(p, protocol.EInternal, "matchmaking unavailable, try again")
			return
		}
		h.mu.Lock()
		p.queued = true
		h.mu.Unlock()
		h.reply(p, protocol.NewMatchmaking(protocol.MatchQueued, position))
		return
	}
	var info cluster.RoomInfo
	info.ID = roomID
	info.Name = pair[0].Nick + "'s room"
	info.Status = protocol.RoomPlaying
	info.CreatedAt = protocol.NowMs()
	info.Owner = h.node
	for i, q := range pair {
		_, online := h.dir.Location(q.PlayerID)
		info.Seats[i] = &cluster.SeatInfo{PlayerID: q.PlayerID, Nick: q.Nick, Connected: online}
		h.toPlayerNode(q.PlayerID, cluster.Envelope{Kind: cluster.KindSeated, Player: q.PlayerID, Room: roomID, Owner: h.node})
	}
	slog.Info("game started", "roomId", roomID, "black", pair[0].PlayerID, "white", pair[1].PlayerID, "node", h.node)
	h.startGame(&roomEntry{info: info}, nil)
}

// MatchmakeCancel removes the caller from the queue; idempotent.
func (h *Hub) MatchmakeCancel(p *Player) {
	h.mu.Lock()
	p.queued = false
	h.mu.Unlock()
	h.dir.Dequeue(p.ID())
	h.reply(p, protocol.NewMatchmaking(protocol.MatchCancelled, 0))
}

// PlayAI seats the caller against an embedded AI at the given difficulty,
// in an isAI room this node owns (PROTOCOL §10). color picks the caller's
// seat: black (default, moves first), white, or random.
func (h *Hub) PlayAI(p *Player, difficulty, color string) {
	switch difficulty {
	case "easy", "normal", "hard":
	default:
		h.replyErr(p, protocol.EInvalidDifficulty, "difficulty must be easy|normal|hard")
		return
	}
	human := 0 // seat index: 0 black, 1 white
	switch color {
	case "", protocol.ColorBlack:
	case protocol.ColorWhite:
		human = 1
	case "random":
		human = randSeat()
	default:
		h.replyErr(p, protocol.EBadRequest, "color must be black|white|random")
		return
	}
	h.mu.Lock()
	if p.roomID != "" {
		h.mu.Unlock()
		h.replyErr(p, protocol.EAlreadyInRoom, "finish your current game first")
		return
	}
	queued := h.takeQueuedLocked(p)
	watch := h.unwatchLocked(p)
	nick := p.Nick()
	h.mu.Unlock()
	h.dequeue(p, queued)
	watch.leave(h, p, true)

	roomID := randID("r", 8)
	if !h.dir.BindPlayer(p.ID(), roomID) {
		h.replyErr(p, protocol.EAlreadyInRoom, "finish your current game first")
		return
	}
	info := cluster.RoomInfo{
		ID:        roomID,
		Name:      nick + " vs AI (" + difficulty + ")",
		Status:    protocol.RoomPlaying,
		IsAI:      true,
		CreatedAt: protocol.NowMs(),
		Owner:     h.node,
	}
	info.Seats[human] = &cluster.SeatInfo{PlayerID: p.ID(), Nick: nick, Connected: true}
	info.Seats[1-human] = &cluster.SeatInfo{PlayerID: randID("ai", 8), Nick: "AI (" + difficulty + ")", IsAI: true, Connected: true}
	h.mu.Lock()
	p.state, p.roomID, p.owner = StatePlaying, roomID, h.node
	h.mu.Unlock()
	h.aiGames.Add(1)
	slog.Info("ai game started", "playerId", p.ID(), "roomId", roomID, "difficulty", difficulty, "humanSeat", human)
	h.startGame(&roomEntry{info: info, ai: difficulty}, nil)
}

// Spectate attaches the caller to a playing room as a viewer (PROTOCOL
// §11): spectate_start with the live game, then every broadcast of that
// game. Watching another room first leaves it; seated players cannot watch.
func (h *Hub) Spectate(p *Player, roomID string) {
	h.mu.Lock()
	inRoom := p.roomID != ""
	h.mu.Unlock()
	if inRoom {
		h.replyErr(p, protocol.EAlreadyInRoom, "leave your current room first")
		return
	}
	info, ok := h.dir.Room(roomID)
	switch {
	case !ok:
		h.replyErr(p, protocol.ERoomNotFound, "room not found")
		return
	case info.Status != protocol.RoomPlaying:
		h.replyErr(p, protocol.ERoomNotPlaying, "only a playing room can be watched")
		return
	}
	h.mu.Lock()
	var prev watchExit
	if p.watching != roomID {
		prev = h.unwatchLocked(p)
	}
	p.watching, p.watchOwner = roomID, info.Owner
	h.mu.Unlock()
	prev.leave(h, p, false) // spectate_start below supersedes it
	h.toNode(info.Owner, cluster.Envelope{Kind: cluster.KindWatch, Player: p.ID(), Room: roomID})
}

// StopSpectating detaches the caller; idempotent (always answers
// spectate_end).
func (h *Hub) StopSpectating(p *Player) {
	h.mu.Lock()
	watch := h.unwatchLocked(p)
	h.mu.Unlock()
	if !watch.leave(h, p, true) {
		h.reply(p, &protocol.SpectateEndMsg{Type: protocol.TSpectateEnd})
	}
}

// Resign forfeits the caller's live game.
func (h *Hub) Resign(p *Player) {
	if roomID, owner, ok := h.liveGame(p); ok {
		h.toNode(owner, cluster.Envelope{Kind: cluster.KindResign, Player: p.ID(), Room: roomID})
		return
	}
	h.replyErr(p, protocol.ENotInRoom, "not in a game")
}

// Move attempts a stone placement in the caller's live game.
func (h *Hub) Move(p *Player, x, y int) {
	if roomID, owner, ok := h.liveGame(p); ok {
		h.toNode(owner, cluster.Envelope{Kind: cluster.KindMove, Player: p.ID(), Room: roomID, X: x, Y: y})
		return
	}
	h.replyErr(p, protocol.ENotInRoom, "not in a game")
}

func (h *Hub) liveGame(p *Player) (roomID, owner string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.state != StatePlaying || p.roomID == "" {
		return "", "", false
	}
	return p.roomID, p.owner, true
}

// AppPing answers an application-level ping (PROTOCOL §1).
func (h *Hub) AppPing(p *Player, ts *int64) {
	v := protocol.NowMs()
	if ts != nil {
		v = *ts
	}
	h.reply(p, &protocol.PongMsg{Type: protocol.TPong, TS: v})
}

// onSessionNotice applies a message about one of this node's sessions.
func (h *Hub) onSessionNotice(e cluster.Envelope) {
	h.mu.Lock()
	p := h.players[e.Player]
	if p == nil {
		h.mu.Unlock()
		return
	}
	switch e.Kind {
	case cluster.KindSeated:
		// Matchmaking or a join put the player in a game owned by e.Owner.
		p.queued = false
		watch := h.unwatchLocked(p)
		p.state, p.roomID, p.owner = StatePlaying, e.Room, e.Owner
		h.mu.Unlock()
		watch.leave(h, p, true)
		return
	case cluster.KindReleased:
		if p.roomID == e.Room {
			p.state, p.roomID, p.owner = StateLobby, "", ""
		}
	case cluster.KindWatchEnd:
		if p.watching == e.Room {
			p.watching, p.watchOwner = "", ""
		}
	case cluster.KindKick:
		// The player opened a newer socket on another node: this session is
		// over here. Dropping it first makes the closing read pump a no-op,
		// so nothing is settled on the player's behalf.
		if p.epoch >= e.Epoch {
			break
		}
		c := p.conn
		p.conn = nil
		delete(h.players, p.ID())
		delete(h.playersByToken, p.Token())
		h.mu.Unlock()
		if c != nil {
			c.Close()
		}
		return
	case cluster.KindResync:
		// The game was restored on e.Owner: re-attach a live socket.
		if p.conn == nil || (p.roomID != "" && p.roomID != e.Room) {
			break
		}
		p.state, p.roomID, p.owner = StatePlaying, e.Room, e.Owner
		epoch := p.epoch
		h.mu.Unlock()
		h.toNode(e.Owner, cluster.Envelope{Kind: cluster.KindReconnect, Player: e.Player, Room: e.Room, Epoch: epoch})
		return
	}
	h.mu.Unlock()
}
