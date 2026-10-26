package ai

import (
	"log/slog"
	"runtime"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

// thinkSlots bounds concurrent searches to the CPU count: the search is
// CPU-bound, so more parallel thinkers would only slow every one of them
// down (each would reach shallower depths in its budget). Excess drivers
// queue for a slot.
var thinkSlots = make(chan struct{}, runtime.GOMAXPROCS(0))

// turnMargin keeps the AI's move clear of its own turn deadline.
const turnMargin = 1500 * time.Millisecond

// Mover is the driver's way back into the game: it posts a move to the
// actor mailbox — the exact path a human client's move takes. game.Actor
// satisfies it.
type Mover interface {
	Move(playerID string, x, y int)
}

// Driver is the AI seat's embedded client: the actor reports every state
// change through Notify, and whenever the state says "AI to move" the
// driver thinks in its own goroutine — never blocking the actor or the hub —
// and posts the chosen cell back through Move.
type Driver struct {
	mover Mover
	eng   *Engine
	aiID  string
	color string // wire color name the AI plays ("black"/"white")
	think time.Duration

	inbox chan *protocol.GameState // cap 1: only the newest state matters
	done  <-chan struct{}          // actor's done: game_over => exit, no leak
}

// NewDriver starts the driver goroutine bound to the actor's lifetime via
// done (game_over closes it). think overrides the preset's per-move budget
// when positive — tests shrink it instead of waiting real seconds.
func NewDriver(m Mover, done <-chan struct{}, aiID, wireColor, difficulty string, think time.Duration) *Driver {
	cfg := For(difficulty)
	if think > 0 {
		cfg.Think = think
	}
	d := &Driver{
		mover: m,
		eng:   NewEngine(cfg),
		aiID:  aiID,
		color: wireColor,
		think: cfg.Think,
		inbox: make(chan *protocol.GameState, 1),
		done:  done,
	}
	go d.loop()
	return d
}

// Notify is the actor's observer hook, called inside the actor goroutine.
// It must never block: irrelevant states are dropped immediately and only
// the newest AI-turn state is queued.
func (d *Driver) Notify(gs *protocol.GameState) {
	if gs.Status != protocol.RoomPlaying || gs.Turn != d.color {
		return
	}
	// Keep only the newest: if the inbox is full, evict the stale state
	// first, then enqueue — the driver never thinks on an obsolete board.
	select {
	case d.inbox <- gs:
		return
	default:
	}
	select {
	case <-d.inbox:
	default:
	}
	select {
	case d.inbox <- gs:
	default:
	}
}

func (d *Driver) loop() {
	for {
		select {
		case <-d.done:
			return
		case gs := <-d.inbox:
			// Coalesce to the newest queued state.
			for {
				select {
				case gs = <-d.inbox:
					continue
				default:
				}
				break
			}
			if gs.Status != protocol.RoomPlaying || gs.Turn != d.color {
				continue
			}
			me := game.Black
			if d.color == protocol.ColorWhite {
				me = game.White
			}
			b := boardFrom(gs)
			select {
			case thinkSlots <- struct{}{}:
			case <-d.done:
				return
			}
			// Budget: the preset's think time, cut short if queueing for a
			// slot ate into the turn clock. Cancellable on actor done: a
			// finished game must not burn the budget.
			deadline := time.Now().Add(d.think)
			if gs.TurnDeadline != nil {
				turnEnd := time.UnixMilli(*gs.TurnDeadline).Add(-turnMargin)
				if turnEnd.Before(deadline) {
					deadline = turnEnd
				}
			}
			x, y, ok := d.eng.MoveCancel(&b, me, deadline, d.done)
			<-thinkSlots
			if ok {
				st := d.eng.Stats()
				slog.Debug("ai move", "aiId", d.aiID, "x", x, "y", y, "reason", st.Reason,
					"depth", st.Depth, "nodes", st.Nodes, "threatNodes", st.VCFNodes, "elapsed", st.Elapsed)
				d.mover.Move(d.aiID, x, y)
			}
		}
	}
}

// boardFrom converts the wire board ([][]{0,1,2}) to game.Board.
func boardFrom(gs *protocol.GameState) game.Board {
	var b game.Board
	for y, row := range gs.Board {
		for x, v := range row {
			switch v {
			case protocol.CellBlack:
				b[y][x] = game.Black
			case protocol.CellWhite:
				b[y][x] = game.White
			}
		}
	}
	return b
}
