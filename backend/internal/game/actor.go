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
// the room and return both players to the lobby.
type OverFunc func(roomID string)

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

	board      Board
	turn       Color
	moveNumber int
	lastMove   *protocol.Pos
	status     string // protocol.RoomPlaying | RoomFinished
	winner     Color
	winReason  string

	seats   [2]*Seat // indexed by Color-1: Black->0, White->1
	turnDL  time.Time
	reconAt map[Color]time.Time // per-side reconnect deadline
	// bothOfflineAt is when the second seat went offline; the verdict is
	// suspended then (PROTOCOL §5) and an abandon timer counts down from it.
	bothOfflineAt time.Time
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
	}
}

// Start launches the actor goroutine; game_start is its first emission.
func (a *Actor) Start() { go a.run() }

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
		a.onOver(a.roomID)
	}()

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
			d := a.bothOfflineAt.Add(a.cfg.AbandonTTL).Sub(time.Now())
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

	switch {
	case a.board.WinFrom(x, y):
		a.finish(c, protocol.ReasonFive)
	case a.board.Full():
		a.finish(Empty, protocol.ReasonDraw)
	default:
		a.turn = c.Other()
		a.turnDL = time.Now().Add(a.cfg.MoveTimeout)
		a.broadcast(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()})
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
}

func (a *Actor) applyReconnect(playerID string) {
	c, ok := a.colorOf(playerID)
	if !ok {
		return
	}
	a.seat(c).Connected = true
	a.bothOfflineAt = time.Time{}
	if a.status != protocol.RoomPlaying {
		return
	}
	// R15/§5 double-disconnect settlement, evaluated on the reconnecting
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
	// Resuming player gets the full state; the opponent hears they are back.
	a.send(playerID, protocol.Marshal(&protocol.GameStateMsg{Type: protocol.TGameState, Game: a.snapshot()}))
	a.send(a.seat(c.Other()).PlayerID, protocol.Marshal(&protocol.OpponentPresenceMsg{
		Type:      protocol.TOpponentPresence,
		Connected: true,
		Deadline:  nil,
	}))
	// If the opponent is also offline, tell the returner about it too.
	if dl, ok := a.reconAt[c.Other()]; ok {
		a.send(playerID, protocol.Marshal(&protocol.OpponentPresenceMsg{
			Type:      protocol.TOpponentPresence,
			Connected: false,
			Deadline:  protocol.Int64Ptr(dl.UnixMilli()),
		}))
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
	a.finish(Empty, protocol.ReasonDisconnectTimeout)
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
}

// ------------------------------------------------------------------ helpers

func (a *Actor) sendGameStart() {
	a.send(a.seat(Black).PlayerID, protocol.Marshal(&protocol.GameStartMsg{
		Type: protocol.TGameStart, Game: a.snapshot(), YouColor: protocol.ColorBlack,
	}))
	a.send(a.seat(White).PlayerID, protocol.Marshal(&protocol.GameStartMsg{
		Type: protocol.TGameStart, Game: a.snapshot(), YouColor: protocol.ColorWhite,
	}))
}

func (a *Actor) broadcast(msg any) {
	data := protocol.Marshal(msg)
	for _, c := range []Color{Black, White} {
		a.send(a.seat(c).PlayerID, data)
	}
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
		Winner:    winner,
		WinReason: reason,
		ServerNow: protocol.NowMs(),
	}
}
