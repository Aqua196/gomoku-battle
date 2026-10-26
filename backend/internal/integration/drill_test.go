package integration

// Failover drill against real server processes (scripts/cluster-drill.sh
// sets it up): two nodes on one Redis; a cross-node game is played until
// the owner is killed with SIGKILL, then the survivor must adopt the game,
// re-attach the surviving player, take the other player back, and finish
// it with every move in the record. Skipped unless DRILL_A is set.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

func TestClusterDrill(t *testing.T) {
	addrA, addrB, kill := os.Getenv("DRILL_A"), os.Getenv("DRILL_B"), os.Getenv("DRILL_KILL_B")
	if addrA == "" || addrB == "" || kill == "" {
		t.Skip("DRILL_A / DRILL_B / DRILL_KILL_B not set — see scripts/cluster-drill.sh")
	}
	a, err := wsclient.Dial(addrA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := wsclient.Dial(addrB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Hello("drill-alice", ""); err != nil {
		t.Fatal(err)
	}
	wb, err := b.Hello("drill-bob", "")
	if err != nil {
		t.Fatal(err)
	}
	ga, _ := pairAcross(t, a, b) // b's node pairs, so B owns the game
	roomID := gameOf(t, ga)["roomId"].(string)
	play(t, a, b, 7, 7, 1)
	play(t, b, a, 0, 0, 2)
	play(t, a, b, 8, 7, 3)
	t.Logf("game %s: 3 moves played, owner is node B; killing it", roomID)

	if out, err := exec.Command("sh", "-c", kill).CombinedOutput(); err != nil { //nolint:gosec // drill command from the operator
		t.Fatalf("kill: %v %s", err, out)
	}
	killed := time.Now()
	st := waitFrameWithin(t, a, "game_state", 20*time.Second)
	t.Logf("node A adopted the game and re-attached alice after %v (lease lapse + recovery scan)", time.Since(killed).Round(time.Millisecond))
	if moveNo(st) != 3 {
		t.Fatalf("restored at move %v", moveNo(st))
	}

	b2, err := wsclient.Dial(addrA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if w, err := b2.Hello("drill-bob", wb["token"].(string)); err != nil || w["resumed"] != true {
		t.Fatalf("bob resume on A: %v %v", w, err)
	}
	waitFrame(t, b2, "game_state", func(m map[string]any) bool { return moveNo(m) == 3 })
	t.Logf("bob reconnected to node A and resumed at move 3")

	for i, mv := range [][2]int{{0, 1}, {9, 7}, {0, 2}, {10, 7}, {0, 3}} {
		mover, other := b2, a
		if i%2 == 1 {
			mover, other = a, b2
		}
		play(t, mover, other, mv[0], mv[1], float64(4+i))
	}
	send(t, a, map[string]any{"type": "move", "x": 11, "y": 7})
	over := waitFrame(t, a, "game_over", nil)
	t.Logf("game_over: winner=%v reason=%v", over["winner"], over["reason"])

	base := strings.Replace(strings.TrimSuffix(addrA, "/ws"), "ws://", "http://", 1)
	var rec protocol.GameRecord
	for deadline := time.Now().Add(3 * time.Second); ; {
		resp, err := http.Get(base + "/api/games/" + roomID)
		if err == nil && resp.StatusCode == http.StatusOK {
			err = json.NewDecoder(resp.Body).Decode(&rec)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatalf("decode record: %v", err)
			}
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("record not found: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rec.Moves) != 9 {
		t.Fatalf("record has %d moves, want 9", len(rec.Moves))
	}
	t.Logf("record %s: %d moves, winner %s — nothing lost across the crash", rec.ID, len(rec.Moves), *rec.Winner)
}

func waitFrameWithin(t *testing.T, c *wsclient.Client, typ string, d time.Duration) map[string]any {
	t.Helper()
	m, err := c.WaitType(typ, d)
	if err != nil {
		t.Fatalf("waiting for %s: %v", typ, err)
	}
	return m
}

// Restart drill: one node, an AI game in progress, the server restarted
// with SIGTERM (DRILL_RESTART_A stops it and starts it again). The game
// must come back — with the AI still playing — as soon as the new process
// is up, without waiting out a lease (graceful shutdown hands it off).
func TestRestartDrill(t *testing.T) {
	addr, restart := os.Getenv("DRILL_A"), os.Getenv("DRILL_RESTART_A")
	if addr == "" || restart == "" {
		t.Skip("DRILL_A / DRILL_RESTART_A not set — see scripts/cluster-drill.sh")
	}
	a, err := wsclient.Dial(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := a.Hello("drill-carol", "")
	if err != nil {
		t.Fatal(err)
	}
	send(t, a, map[string]any{"type": "play_ai", "difficulty": "normal"})
	waitFrame(t, a, "game_start", nil)
	send(t, a, map[string]any{"type": "move", "x": 7, "y": 7})
	waitFrame(t, a, "game_state", func(m map[string]any) bool { return moveNo(m) == 2 })
	t.Logf("AI game at move 2; restarting the server")

	started := time.Now()
	if out, err := exec.Command("sh", "-c", restart).CombinedOutput(); err != nil { //nolint:gosec // drill command from the operator
		t.Fatalf("restart: %v %s", err, out)
	}
	a.Close()
	var a2 *wsclient.Client
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if a2, err = wsclient.Dial(addr, nil); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not come back: %v", err)
		}
	}
	defer a2.Close()
	if w2, err := a2.Hello("drill-carol", w["token"].(string)); err != nil || w2["resumed"] != true {
		t.Fatalf("resume: %v %v", w2, err)
	}
	st := waitFrameWithin(t, a2, "game_state", 10*time.Second)
	t.Logf("game back at move %v, %v after the restart began", moveNo(st), time.Since(started).Round(time.Millisecond))
	if moveNo(st) != 2 {
		t.Fatalf("restored at move %v", moveNo(st))
	}
	send(t, a2, map[string]any{"type": "move", "x": 8, "y": 8})
	waitFrame(t, a2, "game_state", func(m map[string]any) bool { return moveNo(m) == 4 })
	t.Logf("the restored AI answered move 3 — game continues")
}
