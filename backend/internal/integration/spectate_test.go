package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
)

// slowCfg leaves turns long enough that nothing times out mid-test.
func slowCfg() protocol.Config {
	cfg := fastCfg()
	cfg.MoveTimeout = 5 * time.Second
	cfg.ReconnectWindow = 5 * time.Second
	return cfg
}

func send(t *testing.T, c interface{ SendMap(map[string]any) error }, m map[string]any) {
	t.Helper()
	if err := c.SendMap(m); err != nil {
		t.Fatal(err)
	}
}

func TestSpectateFlow(t *testing.T) {
	ts, _ := newTestServer(t, slowCfg())
	a, b, _, _, ga, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()
	roomID := gameOf(t, ga)["roomId"].(string)

	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "carol")
	send(t, c, map[string]any{"type": "spectate", "roomId": roomID})
	start, err := c.WaitType("spectate_start", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if g := gameOf(t, start); g["roomId"] != roomID || g["spectators"].(float64) != 1 {
		t.Fatalf("spectate_start game: %v", g)
	}
	// The players learn about the viewer.
	if st, err := a.WaitType("game_state", 3*time.Second); err != nil || gameOf(t, st)["spectators"].(float64) != 1 {
		t.Fatalf("seat did not see the viewer count: %v %v", st, err)
	}

	send(t, a, map[string]any{"type": "move", "x": 7, "y": 7})
	st, err := c.WaitType("game_state", 3*time.Second)
	if err != nil {
		t.Fatalf("viewer missed the move: %v", err)
	}
	g := gameOf(t, st)
	moves := g["moves"].([]any)
	if g["moveNumber"].(float64) != 1 || len(moves) != 1 {
		t.Fatalf("viewer game_state: move=%v moves=%v", g["moveNumber"], moves)
	}

	send(t, c, map[string]any{"type": "spectate_stop"})
	end, err := c.WaitType("spectate_end", 3*time.Second)
	if err != nil || end["roomId"] != roomID {
		t.Fatalf("spectate_end: %v %v", end, err)
	}
	send(t, b, map[string]any{"type": "move", "x": 8, "y": 8})
	if _, err := b.WaitType("game_state", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, m := range c.Collect(300 * time.Millisecond) {
		if mType(m) == "game_state" {
			t.Fatalf("viewer still receives game_state after spectate_stop: %v", m)
		}
	}
}

func TestSpectateErrors(t *testing.T) {
	ts, _ := newTestServer(t, slowCfg())
	a, b, _, _, ga, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "carol")
	send(t, c, map[string]any{"type": "spectate", "roomId": "r_nope"})
	expectErr(t, c, protocol.ERoomNotFound)
	send(t, c, map[string]any{"type": "spectate"})
	expectErr(t, c, protocol.EBadRequest)

	send(t, c, map[string]any{"type": "create_room", "name": "waiting"})
	created, err := c.WaitType("room", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d := dial(t, ts)
	defer d.Close()
	hello(t, d, "dave")
	send(t, d, map[string]any{"type": "spectate", "roomId": created["room"].(map[string]any)["id"]})
	expectErr(t, d, protocol.ERoomNotPlaying)

	// A seated player cannot watch, not even their own game.
	send(t, a, map[string]any{"type": "spectate", "roomId": gameOf(t, ga)["roomId"]})
	expectErr(t, a, protocol.EAlreadyInRoom)
}

// Starting a game of one's own (here vs the AI) leaves the watched game
// first: spectate_end, then game_start.
func TestSpectatorLeavesWhenPlaying(t *testing.T) {
	cfg := slowCfg()
	cfg.AIThink = 30 * time.Millisecond
	ts, _ := newTestServer(t, cfg)
	a, b, _, _, ga, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()
	roomID := gameOf(t, ga)["roomId"].(string)

	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "carol")
	send(t, c, map[string]any{"type": "spectate", "roomId": roomID})
	if _, err := c.WaitType("spectate_start", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	send(t, c, map[string]any{"type": "play_ai", "difficulty": "easy"})
	end, err := c.WaitType("spectate_end", 3*time.Second)
	if err != nil || end["roomId"] != roomID {
		t.Fatalf("spectate_end: %v %v", end, err)
	}
	if _, err := c.WaitType("game_start", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	// The watched game no longer counts carol.
	send(t, a, map[string]any{"type": "move", "x": 7, "y": 7})
	st, err := a.WaitType("game_state", 3*time.Second)
	for err == nil && gameOf(t, st)["moveNumber"].(float64) != 1 {
		st, err = a.WaitType("game_state", 3*time.Second)
	}
	if err != nil || gameOf(t, st)["spectators"].(float64) != 0 {
		t.Fatalf("spectator count after leaving: %v %v", st, err)
	}
}

// A finished game reaches the viewer as game_over and is then served by
// the replay API with every move, indexed for both players.
func TestRecordedGameAPI(t *testing.T) {
	ts, _ := newTestServer(t, slowCfg())
	a, b, wa, wb, ga, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()
	roomID := gameOf(t, ga)["roomId"].(string)

	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "carol")
	send(t, c, map[string]any{"type": "spectate", "roomId": roomID})
	if _, err := c.WaitType("spectate_start", 3*time.Second); err != nil {
		t.Fatal(err)
	}

	// The viewer joining re-broadcasts game_state (spectators: 1) to both
	// seats; start the scripts from that snapshot.
	sa, err := a.WaitType("game_state", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.WaitType("game_state", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	go func() { _, err := playScriptedWin(a, "black", sa); errs <- err }()
	go func() { _, err := playScriptedWin(b, "white", sb); errs <- err }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	over, err := c.WaitType("game_over", 3*time.Second)
	if err != nil || over["winner"] != "black" {
		t.Fatalf("viewer game_over: %v %v", over, err)
	}

	getJSON := func(path string, v any) int {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			code := resp.StatusCode
			if code == http.StatusOK {
				err = json.NewDecoder(resp.Body).Decode(v)
			}
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			// The record is written right after game_over goes out.
			if code != http.StatusNotFound || time.Now().After(deadline) {
				return code
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	var rec protocol.GameRecord
	if code := getJSON("/api/games/"+roomID, &rec); code != http.StatusOK {
		t.Fatalf("GET game: %d", code)
	}
	if rec.Winner == nil || *rec.Winner != "black" || rec.Reason != "five" || rec.MoveCount != 9 || len(rec.Moves) != 9 {
		t.Fatalf("record: %+v", rec)
	}
	if rec.Moves[0] != (protocol.Pos{X: 7, Y: 7}) || rec.Moves[8] != (protocol.Pos{X: 11, Y: 7}) {
		t.Fatalf("moves out of order: %v", rec.Moves)
	}
	if rec.Black.Nick != "alice" || rec.White.Nick != "bob" {
		t.Fatalf("seats: %+v / %+v", rec.Black, rec.White)
	}

	for _, path := range []string{
		"/api/games",
		"/api/players/" + wa["playerId"].(string) + "/games",
		"/api/players/" + wb["playerId"].(string) + "/games?limit=5",
	} {
		var list protocol.GameList
		if code := getJSON(path, &list); code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, code)
		}
		if len(list.Games) != 1 || list.Games[0].ID != roomID || list.Games[0].Moves != nil {
			t.Fatalf("GET %s: %+v", path, list.Games)
		}
	}
	var none protocol.GameList
	getJSON("/api/players/p_nobody/games", &none)
	if none.Games == nil || len(none.Games) != 0 {
		t.Fatalf("unknown player must list [] not null: %+v", none)
	}
	resp, err := http.Get(ts.URL + "/api/games/r_missing")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing game: %d", resp.StatusCode)
	}
}
