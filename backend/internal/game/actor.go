package game

import (
	"log"
	"runtime/debug"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

// SendFunc delivers one already-marshaled message to one player. The hub
// implements it as a non-blocking write into the player's connection buffer;
// a disconnected player simply drops the message.
type SendFunc func(playerID string, data []byte)

// OverFunc is invoked once when the game finishes; the hub uses it to delete
// the room, return both players to the lobby and record the game.
type OverFunc func(roomID string, res Result)

// Result is the actor's final report. Reason is empty when the actor died
// without a verdict (a recovered panic).
type Result struct {
	Black, White Seat
	Moves        []protocol.Pos
	Winner       Color
	Reason       string
	StartedAt    time.Time
	EndedAt      time.Time
}

// Seat binds one side of the game to a player.
type Seat struct {
	PlayerID  string
	Nick      string
	Connected bool
	IsAI      bool
}

// Actor owns every mutable piece of one game's state: board, turn, both
// deadlines, seats. All mutations arrive as closures on the mailbox and run
// inside a single goroutine, so the state machine is race-free without locks
// (DESIGN D7). Callers post events with Move/Resign/Disconnect/Reconnect;
// posts after the game ends are no-ops because done is closed.
type Actor struct {
	roomID string
	cfg    protocol.Config
	send   SendFunc
	onOver OverFunc

	mailbox chan func()
	done    chan struct{}

	// observer is an optional post-state-change hook (the AI driver uses
	// it); invoked inside the actor goroutine, so it must not block.
	observer func(*protocol.GameState)

	board      Board
	turn       Color
	moveNumber int
	lastMove   *protocol.Pos
	moves      []protocol.Pos // play order; moves[len-1] == *lastMove
	status     string         // protocol.RoomPlaying | RoomFinished
	winner     Color
	winReason  string
	startedAt  time.Time

	// spectators receive every broadcast but have no seat (PROTOCOL §11).
	spectators map[string]struct{}

	seats   [2]*Seat // indexed by Color-1: Black->0, White->1
	turnDL  time.Time
	reconAt map[Color]time.Time // per-side reconnect deadline
	// bothOfflineAt is when the second seat went offline; the verdict is
	// suspended then (PROTOCOL §5) and an abandon timer counts down from it.
	bothOfflineAt time.Time
	moveHook      func(playerID string)
}

// New creates an actor for a room; black moves first.
func New(roomID string, black, white Seat, cfg protocol.Config, send SendFunc, onOver OverFunc) *Actor {
	return &Actor{
		roomID:  roomID,
		cfg:     cfg,
		send:    send,
		onOver:  onOver,
		mailbox: make(chan func(), 128),
		done:    make(chan struct{}),
		turn:    Black,
		status:  protocol.RoomPlaying,
		seats:   [2]*Seat{&black, &white},
		reconAt: make(map[Color]time.Time),

		spectators: make(map[string]struct{}),
	}
}

// Start launches the actor goroutine; game_start is its first emission.
func (a *Actor) Start() { go a.run() }

// SetObserver installs the state-change hook before Start.
func (a *Actor) SetObserver(fn func(*protocol.GameState)) { a.observer = fn }

// Done exposes the actor's completion channel for lifecycle-bound helpers
// (e.g. the AI driver exits when the game ends).
func (a *Actor) Done() <-chan struct{} { return a.done }

// SetMoveHook registers a callback fired once per accepted move — the hub
// uses it for /metrics move counters (M4).
func (a *Actor) SetMoveHook(fn func(playerID string)) { a.moveHook = fn }

// Move queues a stone-placement attempt by playerID at (x,y).
func (a *Actor) Move(playerID string, x, y int) {
	a.post(func() { a.applyMove(playerID, x, y) })
}

// Resign queues a forfeit by playerID.
func (a *Actor) Resign(playerID string) {
	a.post(func() { a.applyResign(playerID) })
}

// Disconnect marks playerID's side offline and opens its reconnect window.
func (a *Actor) Disconnect(playerID string) {
	a.post(func() { a.applyDisconnect(playerID) })
}

// Reconnect marks playerID's side online again and resends the full state.
func (a *Actor) Reconnect(playerID string) {
	a.post(func() { a.applyReconnect(playerID) })
}

// Watch adds a spectator: it gets spectate_start now and every broadcast
// after. Posting after the game ended is a no-op like any other event.
func (a *Actor) Watch(playerID string) {
	a.post(func() { a.applyWatch(playerID) })
}

// Unwatch removes a spectator.
func (a *Actor) Unwatch(playerID string) {
	a.post(func() { a.applyUnwatch(playerID) })
}

func (a *Actor) post(f func()) {
	select {
	case a.mailbox <- f:
	case <-a.done:
	}
}

// ---------------------------------------------------------------- run loop

func (a *Actor) run() {
	var turnT, reconT, abandonT *time.Timer
	var reconC, abandonC <-chan time.Time
	defer func() {
		// R5: a panic must never strand the room or trap both players in
		// StatePlaying — always finish cleanup through onOver.
		if r := recover(); r != nil {
			log.Printf("gomoku: game actor panic room=%s: %v\n%s", a.roomID, r, debug.Stack())
		}
		close(a.done)
		for _, t := range []*time.Timer{turnT, reconT, abandonT} {
			if t != nil {
				stopTimer(t)
			}
		}
		a.onOver(a.roomID, a.result())
	}()

	a.startedAt = time.Now()
	a.turnDL = time.Now().Add(a.cfg.MoveTimeout)
	a.sendGameStart()

	turnT = time.NewTimer(time.Until(a.turnDL))

	for {
		select {
		case f := <-a.mailbox:
			f()
		case <-turnT.C:
			// R6/§5: drain already-arrived events before judging; an
			// on-time move or reconnect must never be lost to select's
			// random pick between two ready cases.
			a.drainMailbox()
			a.onTurnTimeout()
		case <-reconC:
			a.drainMailbox()
			a.onReconTimeout()
		case <-abandonC:
			a.drainMailbox()
			a.onAbandonTimeout()
		}
		if a.status != protocol.RoomPlaying {
			break
		}
		// Re-arm all timers from the current deadlines.
		resetTimer(turnT, time.Until(a.turnDL))
		if dl, ok := a.earliestRecon(); ok {
			d := time.Until(dl)
			if reconT == nil {
				reconT = time.NewTimer(d)
			} else {
				resetTimer(reconT, d)
			}
			reconC = reconT.C
		} else {
			if reconT != nil {
				stopTimer(reconT)
			}
			reconC = nil
		}
		if a.bothOffline() {
			d := time.Until(a.bothOfflineAt.Add(a.cfg.AbandonTTL))
			if abandonT == nil {
				abandonT = time.NewTimer(d)
			} else {
				resetTimer(abandonT, d)
			}
			abandonC = abandonT.C
		} else {
			if abandonT != nil {
				stopTimer(abandonT)
			}
			abandonC = nil
		}
	}
}

// drainMailbox runs every already-queued event, stopping early if the game
// ends mid-drain. Used by the timer cases before judging a timeout.
func (a *Actor) drainMailbox() {
	for {
		select {
		case f := <-a.mailbox:
			f()
		default:
			return
		}
		if a.status != protocol.RoomPlaying {
			return
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	stopTimer(t)
	t.Reset(d)
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func (a *Actor) earliestRecon() (time.Time, bool) {
	var min time.Time
	ok := false
	for _, dl := range a.reconAt {
		if !ok || dl.Before(min) {
			min, ok = dl, true
		}
	}
	return min, ok
}

// ------------------------------------------------------------- event apply

func (a *Actor) applyMove(playerID string, x, y int) {
	c, ok := a.colorOf(playerID)
	if !ok {
		return // not a seat (should not happen; hub filters first)
	}
	if a.status != protocol.RoomPlaying {
		a.sendErr(playerID, protocol.EGameOver, "game is already over")
		return
	}
	if a.turn != c {
		a.sendErr(playerID, protocol.ENotYourTurn, "not your turn")
		return
	}
	if !InBounds(x, y) {
		a.sendErr(playerID, protocol.EIllegalMove, "position out of bounds")
		return
	}
	if !a.board.CanPlace(x, y) {
		a.sendErr(playerID, protocol.EIllegalMove, "cell already occupied")
		return
	}

	a.board.Place(x, y, c)
	a.moveNumber++
	a.lastMove = &protocol.Pos{X: x, Y: y}
	a.moves = append(a.moves, *a.lastMove)
	if a.moveHook != nil {
		a.moveHook(playerID)
	}

	switch {
	case a.board.WinFrom(x, y):
		a.finish(c, protocol.ReasonFive)
	case a.board.Full():
		a.finish(Empty, protocol.ReasonDraw)
	default:
		a.turn = c.Other()
		a.turnDL = time.Now().Add(a.cfg.MoveTimeout)
		a.broadcast(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()})
		a.notify()
	}
}

func (a *Actor) applyResign(playerID string) {
	c, ok := a.colorOf(playerID)
	if !ok || a.status != protocol.RoomPlaying {
		return
	}
	a.finish(c.Other(), protocol.ReasonResign)
}

func (a *Actor) applyDisconnect(playerID string) {
	c, ok := a.colorOf(playerID)
	if !ok {
		return
	}
	a.seat(c).Connected = false
	if a.status != protocol.RoomPlaying {
		return
	}
	a.reconAt[c] = time.Now().Add(a.cfg.ReconnectWindow)
	if !a.seat(c.Other()).Connected {
		a.bothOfflineAt = time.Now() // second seat out: verdict suspends (§5)
	}
	a.send(a.seat(c.Other()).PlayerID, protocol.Marshal(&protocol.OpponentPresenceMsg{
		Type:      protocol.TOpponentPresence,
		Connected: false,
		Deadline:  protocol.Int64Ptr(a.reconAt[c].UnixMilli()),
	}))
	a.toSpectators(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()})
}

func (a *Actor) applyReconnect(playerID string) {
	c, ok := a.colorOf(playerID)
	if !ok {
		return
	}
	wasOffline := !a.seat(c).Connected // a takeover arrives still connected
	a.seat(c).Connected = true
	a.bothOfflineAt = time.Time{}
	if a.status != protocol.RoomPlaying {
		return
	}
	// §5 double-disconnect settlement, evaluated on the reconnecting
	// side's arrival: own window expired -> self loses; else opponent's
	// window expired -> opponent loses; else play continues.
	now := time.Now()
	if dl, ok := a.reconAt[c]; ok && !dl.After(now) {
		a.finish(c.Other(), protocol.ReasonDisconnectTimeout)
		return
	}
	delete(a.reconAt, c)
	if dl, ok := a.reconAt[c.Other()]; ok && !dl.After(now) {
		a.finish(c, protocol.ReasonDisconnectTimeout)
		return
	}
	// Resuming player gets the full state; then its presence view is made
	// authoritative (§5): always told whether the opponent is online.
	a.send(playerID, protocol.Marshal(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()}))
	if dl, ok := a.reconAt[c.Other()]; ok {
		a.send(playerID, protocol.Marshal(&protocol.OpponentPresenceMsg{
			Type:      protocol.TOpponentPresence,
			Connected: false,
			Deadline:  protocol.Int64Ptr(dl.UnixMilli()),
		}))
	} else {
		a.send(playerID, protocol.Marshal(&protocol.OpponentPresenceMsg{
			Type:      protocol.TOpponentPresence,
			Connected: true,
			Deadline:  nil,
		}))
	}
	// The opponent hears "back" only when this seat had actually been
	// offline — a takeover must not emit a phantom reconnect frame.
	if wasOffline {
		a.send(a.seat(c.Other()).PlayerID, protocol.Marshal(&protocol.OpponentPresenceMsg{
			Type:      protocol.TOpponentPresence,
			Connected: true,
			Deadline:  nil,
		}))
		a.toSpectators(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()})
	}
}

func (a *Actor) applyWatch(playerID string) {
	if _, seated := a.colorOf(playerID); seated || a.status != protocol.RoomPlaying {
		return
	}
	a.spectators[playerID] = struct{}{}
	a.send(playerID, protocol.Marshal(&protocol.SpectateStartMsg{Type: protocol.TSpectateStart, Game: a.snapshot()}))
	a.broadcastExcept(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()}, playerID)
}

func (a *Actor) applyUnwatch(playerID string) {
	if _, ok := a.spectators[playerID]; !ok {
		return
	}
	delete(a.spectators, playerID)
	if a.status == protocol.RoomPlaying {
		a.broadcast(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()})
	}
}

// ---------------------------------------------------------------- timeouts

func (a *Actor) onTurnTimeout() {
	if a.status != protocol.RoomPlaying || time.Now().Before(a.turnDL) {
		return // spurious/early fire; the loop re-arms the timer
	}
	a.finish(a.turn.Other(), protocol.ReasonTimeout)
}

func (a *Actor) onReconTimeout() {
	if a.status != protocol.RoomPlaying {
		return
	}
	if a.bothOffline() {
		return // §5: verdict suspended while both sides are offline
	}
	now := time.Now()
	var loser Color
	var loserDL time.Time
	for c, dl := range a.reconAt {
		if !dl.After(now) && (loser == Empty || dl.Before(loserDL)) {
			loser, loserDL = c, dl
		}
	}
	if loser == Empty {
		return // nothing expired yet; timer re-arms
	}
	a.finish(loser.Other(), protocol.ReasonDisconnectTimeout)
}

// onAbandonTimeout retires a game both sides never returned to.
func (a *Actor) onAbandonTimeout() {
	if a.status != protocol.RoomPlaying || !a.bothOffline() {
		return
	}
	a.finish(Empty, protocol.ReasonAbandoned)
}

// bothOffline reports whether both seats are currently disconnected.
func (a *Actor) bothOffline() bool {
	return !a.seat(Black).Connected && !a.seat(White).Connected
}

// ------------------------------------------------------------------ finish

func (a *Actor) finish(winner Color, reason string) {
	a.status = protocol.RoomFinished
	a.winner = winner
	a.winReason = reason
	a.reconAt = nil

	var winnerName *string
	if winner != Empty {
		winnerName = protocol.StrPtr(winner.Name())
	}
	a.broadcast(&protocol.GameOverMsg{
		Type:   protocol.TGameOver,
		Reason: reason,
		Winner: winnerName,
		Game:   a.snapshot(),
	})
	a.notify()
}

// notify reports the newest state to the observer, if any (AI driver).
func (a *Actor) notify() {
	if a.observer != nil {
		a.observer(a.snapshot())
	}
}

// ------------------------------------------------------------------ helpers

func (a *Actor) sendGameStart() {
	a.send(a.seat(Black).PlayerID, protocol.Marshal(&protocol.GameStartMsg{
		Type: protocol.TGameStart, Game: a.snapshot(), YouColor: protocol.ColorBlack,
	}))
	a.send(a.seat(White).PlayerID, protocol.Marshal(&protocol.GameStartMsg{
		Type: protocol.TGameStart, Game: a.snapshot(), YouColor: protocol.ColorWhite,
	}))
	a.notify()
}

// broadcast reaches both seats and every spectator.
func (a *Actor) broadcast(msg any) { a.broadcastExcept(msg, "") }

func (a *Actor) broadcastExcept(msg any, skip string) {
	data := protocol.Marshal(msg)
	for _, c := range []Color{Black, White} {
		if id := a.seat(c).PlayerID; id != skip {
			a.send(id, data)
		}
	}
	for id := range a.spectators {
		if id != skip {
			a.send(id, data)
		}
	}
}

// toSpectators reaches viewers only (seats get opponent_presence instead).
func (a *Actor) toSpectators(msg any) {
	if len(a.spectators) == 0 {
		return
	}
	data := protocol.Marshal(msg)
	for id := range a.spectators {
		a.send(id, data)
	}
}

// result is the final report for OverFunc.
func (a *Actor) result() Result {
	r := Result{
		Black:     *a.seat(Black),
		White:     *a.seat(White),
		Moves:     append([]protocol.Pos(nil), a.moves...),
		StartedAt: a.startedAt,
		EndedAt:   time.Now(),
	}
	if a.status == protocol.RoomFinished {
		r.Winner, r.Reason = a.winner, a.winReason
	}
	return r
}

func (a *Actor) sendErr(playerID, code, message string) {
	a.send(playerID, protocol.Marshal(protocol.NewError(code, message)))
}

func (a *Actor) colorOf(playerID string) (Color, bool) {
	for _, c := range []Color{Black, White} {
		if a.seats[c-1].PlayerID == playerID {
			return c, true
		}
	}
	return Empty, false
}

func (a *Actor) seat(c Color) *Seat { return a.seats[c-1] }

// snapshot builds the wire GameState; only callable inside the actor
// goroutine since it reads unsynchronized fields.
func (a *Actor) snapshot() *protocol.GameState {
	board := make([][]int, BoardSize)
	for y := 0; y < BoardSize; y++ {
		row := make([]int, BoardSize)
		for x := 0; x < BoardSize; x++ {
			row[x] = int(a.board[y][x])
		}
		board[y] = row
	}

	var turnDL *int64
	if a.status == protocol.RoomPlaying {
		turnDL = protocol.Int64Ptr(a.turnDL.UnixMilli())
	}
	var reconDL *int64
	if dl, ok := a.earliestRecon(); ok {
		reconDL = protocol.Int64Ptr(dl.UnixMilli())
	}
	var winner *string
	if a.winner != Empty {
		winner = protocol.StrPtr(a.winner.Name())
	}
	var reason *string
	if a.winReason != "" {
		reason = protocol.StrPtr(a.winReason)
	}

	seatOf := func(c Color) protocol.GamePlayer {
		s := a.seat(c)
		return protocol.GamePlayer{
			PlayerID:  s.PlayerID,
			Nick:      s.Nick,
			Connected: s.Connected,
			IsAI:      s.IsAI,
		}
	}

	return &protocol.GameState{
		RoomID:            a.roomID,
		Status:            a.status,
		Board:             board,
		Turn:              a.turn.Name(),
		MoveNumber:        a.moveNumber,
		LastMove:          a.lastMove,
		TurnDeadline:      turnDL,
		ReconnectDeadline: reconDL,
		Players: protocol.GamePlayers{
			Black: seatOf(Black),
			White: seatOf(White),
		},
		Winner:     winner,
		WinReason:  reason,
		ServerNow:  protocol.NowMs(),
		Moves:      append(make([]protocol.Pos, 0, len(a.moves)), a.moves...),
		Spectators: len(a.spectators),
	}
}
