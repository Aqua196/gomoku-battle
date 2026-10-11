package game

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

func testCfg() protocol.Config {
	return protocol.Config{
		MoveTimeout:     time.Hour, // huge; tests trigger events explicitly
		ReconnectWindow: 50 * time.Millisecond,
		AbandonTTL:      120 * time.Millisecond,
		SendBuf:         64,
	}
}

// capture collects outbound frames per player (thread-safe: the actor's
// send calls it from its own goroutine while tests read).
type capture struct {
	mu     sync.Mutex
	frames map[string][]map[string]any
	over   chan string
}

func newCapture() *capture {
	return &capture{frames: map[string][]map[string]any{}, over: make(chan string, 1)}
}

func (c *capture) send(id string, data []byte) {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames[id] = append(c.frames[id], m)
}

func (c *capture) last(id, typ string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs := c.frames[id]
	for i := len(fs) - 1; i >= 0; i-- {
		if fs[i]["type"] == typ {
			return fs[i]
		}
	}
	return nil
}

func startTestActor(t *testing.T, cfg protocol.Config) (*Actor, *capture, string, string) {
	t.Helper()
	cap := newCapture()
	black := Seat{PlayerID: "p_black", Nick: "black", Connected: true}
	white := Seat{PlayerID: "p_white", Nick: "white", Connected: true}
	a := New("r_test", black, white, cfg, cap.send, func(roomID string) { cap.over <- roomID })
	a.Start()
	return a, cap, "p_black", "p_white"
}

func waitOver(t *testing.T, cap *capture) string {
	t.Helper()
	select {
	case room := <-cap.over:
		return room
	case <-time.After(3 * time.Second):
		t.Fatal("onOver not called")
	}
	return ""
}

// R6: a turn timer firing while the mailbox holds an already-queued move
// must not forfeit — the timer case drains the mailbox first. The test
// stalls the actor on a slow event so the move and the timer are both
// ready at the next select, forcing the drain path.
func TestTimerDrainAppliesQueuedMove(t *testing.T) {
	cfg := testCfg()
	cfg.MoveTimeout = 30 * time.Millisecond
	a, cap, black, _ := startTestActor(t, cfg)

	release := make(chan struct{})
	a.post(func() { <-release }) // occupy the actor while timer fires + move queues
	time.Sleep(50 * time.Millisecond)
	a.Move(black, 7, 7)
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gs := cap.last(black, "game_state"); gs != nil {
			g := gs["game"].(map[string]any)
			if g["turn"] == "white" {
				return // move applied -> turn flipped; no timeout forfeit
			}
		}
		if over := cap.last(black, "game_over"); over != nil {
			t.Fatalf("queued move lost to timer: %v", over)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no game_state after queued move")
}

// R15: with both seats offline the reconnect verdict is suspended; the
// reconnecting player then settles it (opponent's window expired -> win).
func TestDoubleDisconnectSuspendedThenResolved(t *testing.T) {
	cfg := testCfg()
	cfg.ReconnectWindow = 40 * time.Millisecond
	a, cap, black, white := startTestActor(t, cfg)

	a.Disconnect(black)
	time.Sleep(5 * time.Millisecond)
	a.Disconnect(white)

	// Both windows pass while offline: no game_over may appear.
	time.Sleep(100 * time.Millisecond)
	if over := cap.last(black, "game_over"); over != nil {
		t.Fatalf("verdict fired while both offline: %v", over)
	}

	// Black returns: black's own window expired -> black loses, white wins.
	a.Reconnect(black)
	waitOver(t, cap)
	over := cap.last(white, "game_over")
	if over == nil || over["winner"] != "white" || over["reason"] != "disconnect_timeout" {
		t.Fatalf("bad settlement game_over: %v", over)
	}
}

// R15: reconnector inside own window while opponent's window expired.
func TestDoubleDisconnectOpponentExpired(t *testing.T) {
	cfg := testCfg()
	cfg.ReconnectWindow = 40 * time.Millisecond
	a, cap, black, white := startTestActor(t, cfg)

	a.Disconnect(white) // white's window expires first
	time.Sleep(60 * time.Millisecond)
	a.Disconnect(black) // black disconnects with a fresh window
	a.Reconnect(black)  // black returns in-window -> white loses
	waitOver(t, cap)
	over := cap.last(black, "game_over")
	if over == nil || over["winner"] != "black" || over["reason"] != "disconnect_timeout" {
		t.Fatalf("bad settlement game_over: %v", over)
	}
}

// R15 continue-case: both inside their windows -> game resumes normally.
func TestDoubleDisconnectBothInWindowContinues(t *testing.T) {
	cfg := testCfg()
	cfg.ReconnectWindow = 400 * time.Millisecond
	a, cap, black, white := startTestActor(t, cfg)

	a.Disconnect(black)
	a.Disconnect(white)
	time.Sleep(20 * time.Millisecond)
	a.Reconnect(black)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gs := cap.last(black, "game_state"); gs != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if gs := cap.last(black, "game_state"); gs == nil {
		t.Fatal("reconnector must get game_state when the game continues")
	}
	if over := cap.last(black, "game_over"); over != nil {
		t.Fatalf("unexpected game_over: %v", over)
	}
	a.Resign(white) // clean up: black wins by resign
	waitOver(t, cap)
}

// R15 abandon: both sides never come back -> the game is retired.
func TestAbandonedGameRetired(t *testing.T) {
	cfg := testCfg()
	cfg.ReconnectWindow = 30 * time.Millisecond
	cfg.AbandonTTL = 80 * time.Millisecond
	a, cap, black, white := startTestActor(t, cfg)

	a.Disconnect(black)
	a.Disconnect(white)
	waitOver(t, cap)
	over := cap.last(black, "game_over")
	if over == nil || over["reason"] != "abandoned" {
		t.Fatalf("bad abandon game_over: %v", over)
	}
	if w := over["winner"]; w != nil {
		t.Fatalf("abandoned game must have null winner, got %v", w)
	}
}

// R5: a panic inside the mailbox still releases the room via onOver.
func TestActorPanicStillCallsOnOver(t *testing.T) {
	cfg := testCfg()
	cap := newCapture()
	a := New("r_panic",
		Seat{PlayerID: "p_black", Nick: "b", Connected: true},
		Seat{PlayerID: "p_white", Nick: "w", Connected: true},
		cfg, cap.send, func(roomID string) { cap.over <- roomID })
	a.Start()
	a.post(func() { panic("boom") })
	if room := waitOver(t, cap); room != "r_panic" {
		t.Fatalf("onOver room = %q", room)
	}
	// done must be closed so later posts are no-ops.
	select {
	case <-a.done:
	case <-time.After(time.Second):
		t.Fatal("done not closed after panic")
	}
}

// R2/R15 presence regression: A drops, B drops, A returns, B returns — the
// second returner must be told the opponent is back (connected:true), and
// the first returner must have been told the opponent is still offline.
func TestDoubleReconnectPresenceToReturner(t *testing.T) {
	cfg := testCfg()
	cfg.ReconnectWindow = 400 * time.Millisecond
	a, cap, black, white := startTestActor(t, cfg)

	a.Disconnect(black)
	a.Disconnect(white)
	a.Reconnect(black)

	deadline := time.Now().Add(2 * time.Second)
	var presA map[string]any
	for time.Now().Before(deadline) {
		cap.mu.Lock()
		for _, m := range cap.frames[black] {
			if m["type"] == "opponent_presence" {
				presA = m
			}
		}
		cap.mu.Unlock()
		if presA != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if presA == nil || presA["connected"] != false {
		t.Fatalf("A (first returner) should see opponent offline, got %v", presA)
	}

	a.Reconnect(white)
	deadline = time.Now().Add(2 * time.Second)
	var presB map[string]any
	for time.Now().Before(deadline) {
		cap.mu.Lock()
		for _, m := range cap.frames[white] {
			if m["type"] == "opponent_presence" {
				presB = m
			}
		}
		cap.mu.Unlock()
		if presB != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if presB == nil || presB["connected"] != true {
		t.Fatalf("B (second returner) must see opponent online, got %v", presB)
	}
	a.Resign(white)
	waitOver(t, cap)
}
