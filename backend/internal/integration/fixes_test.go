package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
	"github.com/Aqua196/gomoku-battle/backend/internal/ws"
	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
	"github.com/alicebob/miniredis/v2"
)

// R2: hello with a token that belongs to a still-online session takes the
// session over: the old connection is closed and the new one resumes.
func TestHelloTokenTakeover(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a := dial(t, ts)
	defer a.Close()
	w := hello(t, a, "alice")
	token := w["token"].(string)

	b := dial(t, ts)
	defer b.Close()
	w2, err := b.Hello("alice", token)
	if err != nil {
		t.Fatal(err)
	}
	if w2["resumed"] != true || w2["playerId"] != w["playerId"] {
		t.Fatalf("takeover welcome: %v", w2)
	}
	// The old connection must be dead.
	if _, err := a.Read(2 * time.Second); err == nil {
		t.Fatal("old conn still alive after takeover")
	}
	// New conn is fully functional.
	if err := b.SendMap(map[string]any{"type": "list_rooms"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WaitType("room_list", 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

// R1/R2 resumed-without-live-game: a resumed lobby player gets welcome only,
// no game_state — the client clears its view on its own.
func TestResumedInLobbyHasNoGameState(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a := dial(t, ts)
	w := hello(t, a, "alice")
	token := w["token"].(string)
	pid := w["playerId"].(string)
	a.Close()

	b := dial(t, ts)
	defer b.Close()
	w2, err := b.Hello("alice", token)
	if err != nil {
		t.Fatal(err)
	}
	if w2["resumed"] != true || w2["playerId"] != pid {
		t.Fatalf("bad resume: %v", w2)
	}
	if w2["serverNow"].(float64) <= 0 {
		t.Fatalf("welcome missing serverNow: %v", w2)
	}
	// No game_state may follow for a lobby session.
	if _, err := b.WaitType("game_state", 300*time.Millisecond); err == nil {
		t.Fatal("game_state sent for lobby resume")
	}
}

// R3: a burst of lobby mutations coalesces room_list pushes — a healthy
// client is never kicked by backlog and always sees a consistent latest
// snapshot.
func TestRoomListMergeUnderStorm(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	observer := dial(t, ts)
	defer observer.Close()
	hello(t, observer, "obs")

	const storms = 80
	for i := 0; i < storms; i++ {
		c := dial(t, ts)
		hello(t, c, "spawner")
		if err := c.SendMap(map[string]any{"type": "create_room"}); err != nil {
			t.Fatal(err)
		}
		c.Close() // seat vacates on disconnect -> room deleted -> more pushes
	}

	// The observer survived the storm and its latest room_list is
	// consistent with the settled state (zero rooms remain).
	deadline := time.Now().Add(5 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		m, err := observer.WaitType("room_list", 500*time.Millisecond)
		if err != nil {
			break
		}
		last = m
	}
	if last == nil {
		t.Fatal("no room_list received")
	}
	if rooms := last["rooms"].([]any); len(rooms) != 0 {
		t.Fatalf("settled room_list has %d rooms, want 0", len(rooms))
	}
}

// R4: a disconnected non-playing session is expired by the sweeper — its
// token no longer resumes; it issues a fresh player instead.
func TestSessionTTLSweep(t *testing.T) {
	cfg := fastCfg()
	cfg.SessionTTL = 120 * time.Millisecond
	cfg.SweepInterval = 30 * time.Millisecond
	ts, _ := newTestServer(t, cfg)
	a := dial(t, ts)
	w := hello(t, a, "alice")
	token, pid := w["token"].(string), w["playerId"].(string)
	a.Close()
	time.Sleep(200 * time.Millisecond)

	b := dial(t, ts)
	defer b.Close()
	w2, err := b.Hello("alice", token)
	if err != nil {
		t.Fatal(err)
	}
	if w2["resumed"] == true || w2["playerId"] == pid {
		t.Fatalf("expired session should not resume: %v", w2)
	}
}

// R8: a queued player issuing create_room is implicitly dequeued and told.
func TestQueuedPlayerCreateRoomGetsCancelled(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a := dial(t, ts)
	defer a.Close()
	hello(t, a, "alice")
	if err := a.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("matchmaking", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "create_room"}); err != nil {
		t.Fatal(err)
	}
	mm, err := a.WaitType("matchmaking", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if mm["status"] != "cancelled" {
		t.Fatalf("expected cancelled, got %v", mm)
	}
	if _, err := a.WaitType("room", 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

// R12: playing leave_room carries the news through game_over only — the
// leaver does NOT receive a room{left} first.
func TestLeaveRoomPlayingNoRoomLeft(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, _, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	if err := b.SendMap(map[string]any{"type": "leave_room"}); err != nil {
		t.Fatal(err)
	}
	over, err := b.WaitType("game_over", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if over["reason"] != "resign" || over["winner"] != "black" {
		t.Fatalf("bad game_over: %v", over)
	}
	for _, m := range b.Collect(200 * time.Millisecond) {
		if m["type"] == "room" && m["action"] == "left" {
			t.Fatalf("leaver received room{left}: %v", m)
		}
	}
}

// R13: Origin must match Host under the default policy; an absent Origin
// (non-browser client) is allowed.
func TestCheckOrigin(t *testing.T) {
	cfg := fastCfg()
	st := store.NewMemStore()
	h := hub.New(st, cfg)
	srv := ws.NewServer(h, cfg, false) // default policy, NOT allowAny
	ts := httptest.NewServer(srv.Handler("", false))
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	bad := http.Header{"Origin": []string{"http://evil.example.com"}}
	if _, err := wsclient.DialOpts(wsURL, bad, false); err == nil {
		t.Fatal("mismatched origin accepted")
	}
	host := strings.TrimPrefix(ts.URL, "http://")
	good := http.Header{"Origin": []string{"http://" + host}}
	if _, err := wsclient.DialOpts(wsURL, good, false); err != nil {
		t.Fatalf("matching origin rejected: %v", err)
	}
	if _, err := wsclient.DialOpts(wsURL, nil, false); err != nil {
		t.Fatalf("absent origin rejected: %v", err)
	}
}

// welcome/game_state must both expose serverNow (protocol addition).
func TestServerNowFields(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, wa, _, ga, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()
	if wa["serverNow"].(float64) <= 0 {
		t.Fatalf("welcome.serverNow = %v", wa["serverNow"])
	}
	gs := ga["game"].(map[string]any)
	if gs["serverNow"].(float64) <= 0 {
		t.Fatalf("game_state.serverNow = %v", gs["serverNow"])
	}
}

// M4-R1/R2 fix: an in-progress session resumes via the hub's in-memory
// playersByToken index even if Redis goes down mid-game, and Store I/O runs
// outside h.mu.
func TestResumeSurvivesTransientStoreDown(t *testing.T) {
	mr := miniredis.RunT(t)
	st, err := store.NewRedisStore(mr.Addr())
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	cfg := fastCfg()
	cfg.ReconnectWindow = 2 * time.Second
	cfg.MoveTimeout = 5 * time.Second
	h := hub.New(st, cfg)
	t.Cleanup(h.Stop)
	srv := ws.NewServer(h, cfg, true)
	ts := httptest.NewServer(srv.Handler("", false))
	t.Cleanup(ts.Close)

	a, b, wa, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer b.Close()

	// Alice disconnects and Redis goes down before she reconnects.
	a.Close()
	if _, err := b.WaitType("opponent_presence", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	mr.Close()

	a2 := dial(t, ts)
	defer a2.Close()
	w2, err := a2.Hello("alice", wa["token"].(string))
	if err != nil {
		t.Fatalf("reconnect hello while redis down: %v", err)
	}
	if w2["resumed"] != true || w2["playerId"] != wa["playerId"] {
		t.Fatalf("expected in-memory resume despite redis outage, got %v", w2)
	}
	if _, err := a2.WaitType("game_state", 2*time.Second); err != nil {
		t.Fatalf("expected game_state after resume: %v", err)
	}
}
