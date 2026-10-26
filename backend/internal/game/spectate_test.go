package game

import (
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

// waitFor polls until cond holds or fails the test.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Spectators get the live game, every later broadcast, and nothing after
// Unwatch; the seats see the viewer count.
func TestSpectatorLifecycle(t *testing.T) {
	a, cap, black, white := startTestActor(t, testCfg())
	a.Move(black, 7, 7)
	waitFor(t, "first move", func() bool { return cap.last(white, "game_state") != nil })

	a.Watch("p_viewer")
	waitFor(t, "spectate_start", func() bool { return cap.last("p_viewer", "spectate_start") != nil })
	start := cap.last("p_viewer", "spectate_start")["game"].(map[string]any)
	if moves := start["moves"].([]any); len(moves) != 1 || start["spectators"].(float64) != 1 {
		t.Fatalf("spectate_start: moves=%v spectators=%v", start["moves"], start["spectators"])
	}
	waitFor(t, "seats see the viewer", func() bool {
		gs := cap.last(black, "game_state")
		return gs != nil && gs["game"].(map[string]any)["spectators"].(float64) == 1
	})

	mark := cap.mark("p_viewer")
	a.Move(white, 8, 8)
	waitFor(t, "viewer sees the move", func() bool { return len(cap.framesSince("p_viewer", mark)) > 0 })

	a.Watch(black) // a seat cannot also watch
	a.Unwatch("p_viewer")
	waitFor(t, "count drops", func() bool {
		gs := cap.last(white, "game_state")
		return gs["game"].(map[string]any)["spectators"].(float64) == 0
	})
	mark = cap.mark("p_viewer")
	a.Move(black, 7, 8)
	waitFor(t, "move applied", func() bool {
		gs := cap.last(white, "game_state")
		return gs["game"].(map[string]any)["moveNumber"].(float64) == 3
	})
	if got := cap.framesSince("p_viewer", mark); len(got) != 0 {
		t.Fatalf("unwatched viewer still receives frames: %v", got)
	}
}

// The final report carries every move in order and the verdict.
func TestResultRecordsMoves(t *testing.T) {
	cap := newCapture()
	results := make(chan Result, 1)
	a := New("r_rec", Seat{PlayerID: "b", Connected: true}, Seat{PlayerID: "w", Connected: true},
		testCfg(), cap.send, func(_ string, r Result) { results <- r })
	a.Start()
	script := [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}, {2, 0}, {2, 1}, {3, 0}, {3, 1}, {4, 0}}
	for i, m := range script {
		id := "b"
		if i%2 == 1 {
			id = "w"
		}
		a.Move(id, m[0], m[1])
	}
	select {
	case r := <-results:
		if r.Reason != protocol.ReasonFive || r.Winner != Black {
			t.Fatalf("verdict %q winner %v", r.Reason, r.Winner)
		}
		if len(r.Moves) != len(script) {
			t.Fatalf("recorded %d moves, want %d", len(r.Moves), len(script))
		}
		for i, m := range script {
			if r.Moves[i] != (protocol.Pos{X: m[0], Y: m[1]}) {
				t.Fatalf("move %d = %v, want %v", i, r.Moves[i], m)
			}
		}
		if r.StartedAt.IsZero() || r.EndedAt.Before(r.StartedAt) {
			t.Fatalf("bad timestamps %v .. %v", r.StartedAt, r.EndedAt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result")
	}
}
