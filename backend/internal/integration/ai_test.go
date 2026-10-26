package integration

import (
	"runtime"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

// aiCfg keeps AI moves fast but leaves the human's turn timer generous so
// slow AI thinking can't forfeit the game.
func aiCfg(think time.Duration) protocol.Config {
	cfg := fastCfg()
	cfg.MoveTimeout = 10 * time.Second
	cfg.ReconnectWindow = 10 * time.Second
	cfg.AIThink = think
	return cfg
}

// TestPlayAIFlow: play_ai creates an isAI game where the human is black;
// after the human moves, the AI replies through the same actor path.
func TestPlayAIFlow(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(80*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")

	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "normal"}); err != nil {
		t.Fatal(err)
	}
	gs, err := a.WaitType("game_start", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if gs["youColor"] != "black" {
		t.Fatalf("youColor = %v", gs["youColor"])
	}
	g := gs["game"].(map[string]any)
	white := g["players"].(map[string]any)["white"].(map[string]any)
	if white["isAI"] != true || white["connected"] != true {
		t.Fatalf("bad AI seat: %v", white)
	}

	// Human plays center; AI must answer inside its think budget.
	if err := a.SendMap(map[string]any{"type": "move", "x": 7, "y": 7}); err != nil {
		t.Fatal(err)
	}
	st, err := a.WaitType("game_state", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	gg := st["game"].(map[string]any)
	if gg["turn"] != "white" || gg["moveNumber"].(float64) != 1 {
		t.Fatalf("after human move: %v", gg)
	}
	st, err = a.WaitType("game_state", 3*time.Second)
	if err != nil {
		t.Fatalf("AI did not move: %v", err)
	}
	gg = st["game"].(map[string]any)
	if gg["turn"] != "black" || gg["moveNumber"].(float64) != 2 {
		t.Fatalf("after AI move: %v", gg)
	}
	lm := gg["lastMove"].(map[string]any)
	if _, ok := lm["x"].(float64); !ok {
		t.Fatalf("AI lastMove malformed: %v", lm)
	}
}

func TestPlayAIInvalidDifficulty(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(0))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "impossible"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.EInvalidDifficulty)
}

// TestPlayAIWhileInRoom: play_ai is rejected while already in a game.
func TestPlayAIWhileInRoom(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(50*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "easy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_start", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "hard"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.EAlreadyInRoom)
}

// TestAINonBlocking: while the AI is thinking (budget ~1.2s), other clients
// matchmake and move without any slowdown.
func TestAINonBlocking(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(1200*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "hard"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_start", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "move", "x": 7, "y": 7}); err != nil {
		t.Fatal(err)
	}
	// AI now thinks for up to ~1.2s.

	start := time.Now()
	b, c := dial(t, ts), dial(t, ts)
	defer b.Close()
	defer c.Close()
	hello(t, b, "bob")
	hello(t, c, "carol")
	if err := b.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WaitType("matchmaking", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	gb, err := b.WaitType("game_start", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitType("game_start", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if gb["youColor"] == "black" {
		if err := b.SendMap(map[string]any{"type": "move", "x": 5, "y": 5}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.WaitType("game_state", 2*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el > 1200*time.Millisecond {
		t.Fatalf("hub blocked by AI thinking for %v", el)
	}
}

// TestAIResign: resigning vs AI ends the game and returns the human to
// lobby (room gone, AI driver exits via actor done).
func TestAIResign(t *testing.T) {
	ts, h := newTestServer(t, aiCfg(60*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "easy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_start", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "resign"}); err != nil {
		t.Fatal(err)
	}
	over, err := a.WaitType("game_over", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if over["reason"] != "resign" || over["winner"] != "white" {
		t.Fatalf("bad game_over: %v", over)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m := h.Metrics()
		if m.GamesPlayed == 1 && m.ActiveGames == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("metrics after AI resign: %+v", h.Metrics())
}

// TestAIQueuedFrameOrder: a queued player's play_ai must emit
// matchmaking{cancelled} BEFORE game_start.
func TestAIQueuedFrameOrder(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(50*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("matchmaking", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "easy"}); err != nil {
		t.Fatal(err)
	}
	m1, err := a.Read(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if m1["type"] != "matchmaking" || m1["status"] != "cancelled" {
		t.Fatalf("first frame = %v, want matchmaking{cancelled}", m1)
	}
	m2, err := a.Read(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if m2["type"] != "game_start" {
		t.Fatalf("second frame = %v, want game_start", m2)
	}
}

// TestAIHumanDisconnectTimeout: the human's reconnect window still applies
// in AI games; expiry forfeits the human and cleans the room.
func TestAIHumanDisconnectTimeout(t *testing.T) {
	cfg := aiCfg(30 * time.Millisecond)
	cfg.ReconnectWindow = 150 * time.Millisecond
	ts, h := newTestServer(t, cfg)
	a := dial(t, ts)
	w := hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "easy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_start", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	a.Close() // human vanishes; AI stays "connected"
	_ = w

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m := h.Metrics()
		if m.GamesPlayed == 1 && m.ActiveGames == 0 && m.Rooms == 0 {
			return // room cleaned, game counted
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("game not cleaned after human timeout: %+v", h.Metrics())
}

// TestAIDriverNoLeak: after game_over the driver goroutine exits — total
// goroutines fall back to (near) the pre-game baseline.
func TestAIDriverNoLeak(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(40*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	base := runtime.NumGoroutine()

	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "hard"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_start", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "move", "x": 7, "y": 7}); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "resign"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_over", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines after game_over = %d, baseline %d", runtime.NumGoroutine(), base)
}

// TestPlayAIAsWhite: color:"white" seats the human second; the AI opens as
// black without any human input, then waits for the human's reply.
func TestPlayAIAsWhite(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(60*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "easy", "color": "white"}); err != nil {
		t.Fatal(err)
	}
	gs, err := a.WaitType("game_start", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if gs["youColor"] != "white" {
		t.Fatalf("youColor = %v, want white", gs["youColor"])
	}
	black := gs["game"].(map[string]any)["players"].(map[string]any)["black"].(map[string]any)
	if black["isAI"] != true {
		t.Fatalf("black seat should be the AI: %v", black)
	}
	st, err := a.WaitType("game_state", 3*time.Second)
	if err != nil {
		t.Fatalf("AI did not open: %v", err)
	}
	g := st["game"].(map[string]any)
	if g["turn"] != "white" || g["moveNumber"].(float64) != 1 {
		t.Fatalf("after AI opening: turn=%v move=%v", g["turn"], g["moveNumber"])
	}
	// The human (white) can now answer; the AI replies again.
	lm := g["lastMove"].(map[string]any)
	x, y := int(lm["x"].(float64)), int(lm["y"].(float64))
	if err := a.SendMap(map[string]any{"type": "move", "x": (x + 1) % 15, "y": y}); err != nil {
		t.Fatal(err)
	}
	for {
		st, err = a.WaitType("game_state", 3*time.Second)
		if err != nil {
			t.Fatalf("AI did not reply: %v", err)
		}
		if st["game"].(map[string]any)["moveNumber"].(float64) == 3 {
			return
		}
	}
}

// TestPlayAIColorRandomAndInvalid: random lands on a real seat; an unknown
// color is a bad_request.
func TestPlayAIColorRandomAndInvalid(t *testing.T) {
	ts, _ := newTestServer(t, aiCfg(40*time.Millisecond))
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "easy", "color": "purple"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.EBadRequest)

	if err := a.SendMap(map[string]any{"type": "play_ai", "difficulty": "easy", "color": "random"}); err != nil {
		t.Fatal(err)
	}
	gs, err := a.WaitType("game_start", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c := gs["youColor"]; c != "black" && c != "white" {
		t.Fatalf("youColor = %v", c)
	}
}
