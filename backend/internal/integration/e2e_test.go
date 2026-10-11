// Package integration runs the M1 end-to-end suite: real httptest servers,
// real gorilla websocket clients, the frozen PROTOCOL.md contract. Time
// constants are injected small so windows and timeouts stay deterministic
// without sleeping on real 30s values.
package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
	"github.com/Aqua196/gomoku-battle/backend/internal/ws"
	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

// fastCfg is the timing profile for behavioral tests: windows long enough to
// be deterministic on a busy CI box, short enough to keep tests fast.
func fastCfg() protocol.Config {
	return protocol.Config{
		HelloTimeout:    300 * time.Millisecond,
		WSPingPeriod:    200 * time.Millisecond,
		WSPongWait:      10 * time.Second,
		WriteWait:       2 * time.Second,
		MoveTimeout:     250 * time.Millisecond,
		ReconnectWindow: 400 * time.Millisecond,
		SendBuf:         64,
		SessionTTL:      300 * time.Millisecond,
		SweepInterval:   50 * time.Millisecond,
		AbandonTTL:      600 * time.Millisecond,
	}
}

func newTestServer(t *testing.T, cfg protocol.Config) (*httptest.Server, *hub.Hub) {
	t.Helper()
	st := store.NewMemStore()
	h := hub.New(st, cfg)
	srv := ws.NewServer(h, cfg, true)
	ts := httptest.NewServer(srv.Handler("", false))
	t.Cleanup(ts.Close)
	return ts, h
}

func dial(t *testing.T, ts *httptest.Server) *wsclient.Client {
	t.Helper()
	c, err := wsclient.Dial(ts.URL+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func hello(t *testing.T, c *wsclient.Client, nick string) map[string]any {
	t.Helper()
	w, err := c.Hello(nick, "")
	if err != nil {
		t.Fatalf("hello %q: %v", nick, err)
	}
	return w
}

// startMatchGame dials two players, hellos them, queues both and returns the
// clients plus their welcome and game_start frames (A is always black).
func startMatchGame(t *testing.T, ts *httptest.Server, nickA, nickB string) (a, b *wsclient.Client, wa, wb, ga, gb map[string]any) {
	t.Helper()
	a, b = dial(t, ts), dial(t, ts)
	wa = hello(t, a, nickA)
	wb = hello(t, b, nickB)
	if err := a.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	// Wait for A's queue ack so A is always FIFO-first -> black.
	if _, err := a.WaitType("matchmaking", 3*time.Second); err != nil {
		t.Fatalf("A matchmaking ack: %v", err)
	}
	if err := b.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	var err error
	if ga, err = a.WaitType("game_start", 3*time.Second); err != nil {
		t.Fatalf("A game_start: %v", err)
	}
	if gb, err = b.WaitType("game_start", 3*time.Second); err != nil {
		t.Fatalf("B game_start: %v", err)
	}
	if ga["youColor"] != "black" || gb["youColor"] != "white" {
		t.Fatalf("A must be black, B white: %v %v", ga["youColor"], gb["youColor"])
	}
	return a, b, wa, wb, ga, gb
}

func mType(m map[string]any) string { s, _ := m["type"].(string); return s }

func mErrCode(m map[string]any) string { s, _ := m["code"].(string); return s }

func expectErr(t *testing.T, c *wsclient.Client, code string) {
	t.Helper()
	m, err := c.WaitType("error", 3*time.Second)
	if err != nil {
		t.Fatalf("waiting for error %q: %v", code, err)
	}
	if mErrCode(m) != code {
		t.Fatalf("error code = %q, want %q (frame %v)", mErrCode(m), code, m)
	}
}

func gameOf(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	g, ok := frame["game"].(map[string]any)
	if !ok {
		t.Fatalf("frame has no game object: %v", frame)
	}
	return g
}

// playScriptedWin drives a game to completion by answering every turn whose
// turn field equals myColor with the next scripted move. Black wins with
// five across row 7; white plays harmless filler. Returns (game_over, err)
// so goroutine callers can report failures without touching *testing.T.
func playScriptedWin(c *wsclient.Client, myColor string, first map[string]any) (map[string]any, error) {
	blackMoves := [][2]int{{7, 7}, {8, 7}, {9, 7}, {10, 7}, {11, 7}}
	whiteMoves := [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	idx := 0
	cur := first
	awaitEcho := false // one in-flight move at a time
	for {
		g, _ := cur["game"].(map[string]any)
		if !awaitEcho && g != nil && g["turn"] == myColor {
			moves := blackMoves
			if myColor == "white" {
				moves = whiteMoves
			}
			if idx >= len(moves) {
				return nil, fmt.Errorf("%s script exhausted without game_over", myColor)
			}
			mv := moves[idx]
			idx++
			if err := c.SendMap(map[string]any{"type": "move", "x": mv[0], "y": mv[1]}); err != nil {
				return nil, fmt.Errorf("send move: %w", err)
			}
			awaitEcho = true
		}
		m, err := c.Read(10 * time.Second)
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		switch m["type"] {
		case "game_state":
			cur = m
			awaitEcho = false
		case "game_over":
			return m, nil
		case "error":
			return nil, fmt.Errorf("%s got error frame: %v", myColor, m)
		}
	}
}

// ---------------------------------------------------------------- hello

func TestWSHelloWelcome(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	c := dial(t, ts)
	defer c.Close()

	w := hello(t, c, "alice")
	if pid, _ := w["playerId"].(string); !strings.HasPrefix(pid, "p_") {
		t.Fatalf("playerId = %v, want p_*", w["playerId"])
	}
	if tok, _ := w["token"].(string); len(tok) < 22 {
		t.Fatalf("token too short: %q", w["token"])
	}
	if w["resumed"] != false {
		t.Fatal("fresh hello must not be resumed")
	}
	if w["nick"] != "alice" {
		t.Fatalf("nick = %v", w["nick"])
	}
}

func TestWSHelloTimeout(t *testing.T) {
	cfg := fastCfg()
	cfg.HelloTimeout = 150 * time.Millisecond
	ts, _ := newTestServer(t, cfg)
	c := dial(t, ts)
	defer c.Close()

	m, err := c.WaitType("error", 2*time.Second)
	if err != nil {
		t.Fatalf("no hello_timeout error: %v", err)
	}
	if mErrCode(m) != protocol.EHelloTimeout {
		t.Fatalf("code = %q, want hello_timeout", mErrCode(m))
	}
	if _, err := c.Read(2 * time.Second); err == nil {
		t.Fatal("connection should be closed after hello_timeout")
	}
}

func TestWSHelloValidation(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	c := dial(t, ts)
	defer c.Close()

	// Known non-hello type before identification -> unauthorized.
	if err := c.SendMap(map[string]any{"type": "list_rooms"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, c, protocol.EUnauthorized)

	// Malformed JSON -> bad_request.
	bad := dial(t, ts)
	defer bad.Close()
	if err := bad.SendRaw("{not json"); err != nil {
		t.Fatal(err)
	}
	expectErr(t, bad, protocol.EBadRequest)

	// Empty / whitespace nick -> invalid_nick.
	for _, nick := range []string{"", "   "} {
		if err := bad.SendMap(map[string]any{"type": "hello", "nick": nick}); err != nil {
			t.Fatal(err)
		}
		expectErr(t, bad, protocol.EInvalidNick)
	}

	// A proper hello still succeeds afterwards on the first client.
	if w := hello(t, c, "carol"); w["type"] != "welcome" {
		t.Fatalf("expected welcome, got %v", w)
	}
}

// ----------------------------------------------------------------- rooms

func TestRoomCreateJoinList(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b := dial(t, ts), dial(t, ts)
	defer a.Close()
	defer b.Close()
	hello(t, a, "alice")
	hello(t, b, "bob")

	if err := a.SendMap(map[string]any{"type": "create_room", "name": "my room"}); err != nil {
		t.Fatal(err)
	}
	created, err := a.WaitType("room", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if created["action"] != "created" {
		t.Fatalf("action = %v", created["action"])
	}
	room := created["room"].(map[string]any)
	if room["status"] != "waiting" || room["name"] != "my room" || room["isAI"] != false {
		t.Fatalf("bad room payload: %v", room)
	}
	players := room["players"].([]any)
	if len(players) != 1 || players[0].(map[string]any)["color"] != "black" {
		t.Fatalf("creator must be seated black: %v", players)
	}
	roomID := room["id"].(string)

	// Lobby player B receives the pushed room_list containing the room.
	list, err := b.WaitType("room_list", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range list["rooms"].([]any) {
		if r.(map[string]any)["id"] == roomID {
			found = true
		}
	}
	if !found {
		t.Fatalf("room_list missing %s: %v", roomID, list)
	}

	// B joins -> game starts for both.
	if err := b.SendMap(map[string]any{"type": "join_room", "roomId": roomID}); err != nil {
		t.Fatal(err)
	}
	gs, err := a.WaitType("game_start", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	gs2, err := b.WaitType("game_start", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if gs["youColor"] != "black" || gs2["youColor"] != "white" {
		t.Fatalf("youColor wrong: A=%v B=%v", gs["youColor"], gs2["youColor"])
	}
	g := gameOf(t, gs)
	if g["roomId"] != roomID || g["status"] != "playing" || g["turn"] != "black" {
		t.Fatalf("bad game state: %v", g)
	}
	if g["moveNumber"].(float64) != 0 {
		t.Fatalf("moveNumber = %v", g["moveNumber"])
	}
	if g["lastMove"] != nil {
		t.Fatalf("lastMove should be null: %v", g["lastMove"])
	}
	if g["turnDeadline"] == nil {
		t.Fatal("turnDeadline must be set while playing")
	}
}

func TestRoomErrors(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, c := dial(t, ts), dial(t, ts), dial(t, ts)
	defer a.Close()
	defer b.Close()
	defer c.Close()
	hello(t, a, "alice")
	hello(t, b, "bob")
	hello(t, c, "carol")

	if err := a.SendMap(map[string]any{"type": "join_room", "roomId": "r_nope"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.ERoomNotFound)

	if err := a.SendMap(map[string]any{"type": "leave_room"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.ENotInRoom)

	if err := a.SendMap(map[string]any{"type": "create_room"}); err != nil {
		t.Fatal(err)
	}
	room, err := a.WaitType("room", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	roomID := room["room"].(map[string]any)["id"].(string)

	// Creator tries a second room / joins elsewhere -> already_in_room.
	if err := a.SendMap(map[string]any{"type": "create_room"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.EAlreadyInRoom)
	if err := a.SendMap(map[string]any{"type": "join_room", "roomId": roomID}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.EAlreadyInRoom)

	// B joins -> room full for C.
	if err := b.SendMap(map[string]any{"type": "join_room", "roomId": roomID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_start", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WaitType("game_start", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMap(map[string]any{"type": "join_room", "roomId": roomID}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, c, protocol.ERoomFull)
}

func TestRoomLeaveWaiting(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b := dial(t, ts), dial(t, ts)
	defer a.Close()
	defer b.Close()
	hello(t, a, "alice")
	hello(t, b, "bob")

	if err := a.SendMap(map[string]any{"type": "create_room"}); err != nil {
		t.Fatal(err)
	}
	room, err := a.WaitType("room", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	roomID := room["room"].(map[string]any)["id"].(string)

	if err := a.SendMap(map[string]any{"type": "leave_room"}); err != nil {
		t.Fatal(err)
	}
	left, err := a.WaitType("room", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if left["action"] != "left" || left["roomId"] != roomID {
		t.Fatalf("bad left frame: %v", left)
	}

	// The room is gone for everyone. Older broadcast lists may sit ahead
	// of the fresh one in B's buffer, so read until one lacks the room.
	if err := b.SendMap(map[string]any{"type": "list_rooms"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		list, err := b.WaitType("room_list", time.Until(deadline))
		if err != nil {
			t.Fatalf("waiting room_list: %v", err)
		}
		present := false
		for _, r := range list["rooms"].([]any) {
			if r.(map[string]any)["id"] == roomID {
				present = true
			}
		}
		if !present {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("room %s still listed: %v", roomID, list)
		}
	}
}

// -------------------------------------------------------------- matchmake

func TestMatchmakeAndCancel(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, c := dial(t, ts), dial(t, ts), dial(t, ts)
	defer a.Close()
	defer b.Close()
	defer c.Close()
	hello(t, a, "alice")
	hello(t, b, "bob")
	hello(t, c, "carol")

	if err := a.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	mm, err := a.WaitType("matchmaking", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if mm["status"] != "queued" || mm["position"].(float64) != 1 {
		t.Fatalf("bad queued frame: %v", mm)
	}

	if err := b.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_start", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WaitType("game_start", 3*time.Second); err != nil {
		t.Fatal(err)
	}

	if err := c.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitType("matchmaking", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMap(map[string]any{"type": "matchmake_cancel"}); err != nil {
		t.Fatal(err)
	}
	mc, err := c.WaitType("matchmaking", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if mc["status"] != "cancelled" {
		t.Fatalf("status = %v", mc["status"])
	}
}

func TestMatchmakeRequiresLobby(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, _, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	if err := a.SendMap(map[string]any{"type": "matchmake"}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.EAlreadyInRoom)
}

// M2: play_ai is implemented; the not_implemented path no longer exists.
// Difficulty validation is covered by TestPlayAIInvalidDifficulty.

// ------------------------------------------------------------------ game

func TestGamePlayToFive(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, _, _, ga, gb := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	var overA, overB map[string]any
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); overA, errA = playScriptedWin(a, "black", ga) }()
	go func() { defer wg.Done(); overB, errB = playScriptedWin(b, "white", gb) }()
	wg.Wait()
	if errA != nil || errB != nil {
		t.Fatalf("scripted game failed: A=%v B=%v", errA, errB)
	}

	for _, over := range []map[string]any{overA, overB} {
		if over["reason"] != "five" || over["winner"] != "black" {
			t.Fatalf("bad game_over: %v", over)
		}
		g := gameOf(t, over)
		if g["status"] != "finished" || g["winner"] != "black" || g["winReason"] != "five" {
			t.Fatalf("bad final game state: %v", g)
		}
		board := g["board"].([]any)
		if len(board) != 15 || len(board[0].([]any)) != 15 {
			t.Fatalf("board shape wrong: %v", len(board))
		}
		if board[7].([]any)[7].(float64) != 1 {
			t.Fatalf("board[7][7] should be black(1): %v", board[7])
		}
		if g["turnDeadline"] != nil {
			t.Fatalf("turnDeadline must be null when finished: %v", g["turnDeadline"])
		}
	}
}

func TestMoveErrors(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, _, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	// White moving first -> not_your_turn.
	if err := b.SendMap(map[string]any{"type": "move", "x": 0, "y": 0}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, b, protocol.ENotYourTurn)

	// Out of bounds -> illegal_move.
	if err := a.SendMap(map[string]any{"type": "move", "x": 15, "y": 0}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.EIllegalMove)

	// Legal move; then A moving again -> not_your_turn.
	if err := a.SendMap(map[string]any{"type": "move", "x": 7, "y": 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WaitType("game_state", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMap(map[string]any{"type": "move", "x": 7, "y": 8}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, a, protocol.ENotYourTurn)

	// Occupied cell -> illegal_move for B.
	if err := b.SendMap(map[string]any{"type": "move", "x": 7, "y": 7}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, b, protocol.EIllegalMove)

	// A lobby player has no game -> not_in_room.
	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "carol")
	if err := c.SendMap(map[string]any{"type": "move", "x": 1, "y": 1}); err != nil {
		t.Fatal(err)
	}
	expectErr(t, c, protocol.ENotInRoom)
}

func TestResign(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, _, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	if err := b.SendMap(map[string]any{"type": "resign"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*wsclient.Client{a, b} {
		over, err := c.WaitType("game_over", 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if over["reason"] != "resign" || over["winner"] != "black" {
			t.Fatalf("bad game_over: %v", over)
		}
	}
}

func TestLeaveRoomDuringPlayingIsResign(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	a, b, _, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	if err := b.SendMap(map[string]any{"type": "leave_room"}); err != nil {
		t.Fatal(err)
	}
	over, err := a.WaitType("game_over", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if over["reason"] != "resign" || over["winner"] != "black" {
		t.Fatalf("bad game_over: %v", over)
	}
}

func TestTurnTimeout(t *testing.T) {
	cfg := fastCfg()
	cfg.MoveTimeout = 150 * time.Millisecond
	ts, _ := newTestServer(t, cfg)
	a, b, _, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer a.Close()
	defer b.Close()

	// Black never moves; after ~150ms both see timeout, white wins.
	for _, c := range []*wsclient.Client{a, b} {
		over, err := c.WaitType("game_over", 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if over["reason"] != "timeout" || over["winner"] != "white" {
			t.Fatalf("bad timeout game_over: %v", over)
		}
	}
}

// ------------------------------------------------------------- reconnect

func TestReconnectResume(t *testing.T) {
	cfg := fastCfg()
	cfg.ReconnectWindow = 800 * time.Millisecond
	ts, _ := newTestServer(t, cfg)
	a, b, wa, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer b.Close()

	a.Close() // black disconnects

	pres, err := b.WaitType("opponent_presence", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if pres["connected"] != false {
		t.Fatalf("presence = %v", pres)
	}
	dl, ok := pres["deadline"].(float64)
	if !ok || dl <= float64(protocol.NowMs()) {
		t.Fatalf("presence deadline bogus: %v", pres)
	}

	// Reconnect within the window: new conn, hello with token.
	a2 := dial(t, ts)
	defer a2.Close()
	if err := a2.SendMap(map[string]any{"type": "hello", "nick": "alice", "token": wa["token"]}); err != nil {
		t.Fatal(err)
	}
	// Protocol order: welcome(resumed) THEN game_state.
	m1, err := a2.Read(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if mType(m1) != "welcome" || m1["resumed"] != true {
		t.Fatalf("first frame must be welcome resumed: %v", m1)
	}
	if m1["playerId"] != wa["playerId"] {
		t.Fatalf("resumed playerId changed: %v", m1)
	}
	m2, err := a2.Read(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if mType(m2) != "game_state" {
		t.Fatalf("second frame must be game_state: %v", m2)
	}
	g := gameOf(t, m2)
	if g["reconnectDeadline"] != nil {
		t.Fatalf("reconnectDeadline must clear after resume: %v", g)
	}
	pl := g["players"].(map[string]any)
	if pl["black"].(map[string]any)["connected"] != true {
		t.Fatalf("black must be connected in state: %v", pl)
	}

	// Opponent hears black is back.
	pres2, err := b.WaitType("opponent_presence", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if pres2["connected"] != true || pres2["deadline"] != nil {
		t.Fatalf("presence(true) = %v", pres2)
	}

	// The game continues normally: black moves, both see game_state.
	if err := a2.SendMap(map[string]any{"type": "move", "x": 7, "y": 7}); err != nil {
		t.Fatal(err)
	}
	gs, err := a2.WaitType("game_state", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if lm := gameOf(t, gs)["lastMove"].(map[string]any); lm["x"].(float64) != 7 || lm["y"].(float64) != 7 {
		t.Fatalf("lastMove wrong after resume: %v", lm)
	}
}

func TestReconnectTimeout(t *testing.T) {
	cfg := fastCfg()
	// Timers are independent and first-to-fire wins (PROTOCOL §139), so
	// make the reconnect window much shorter than the move clock.
	cfg.ReconnectWindow = 250 * time.Millisecond
	cfg.MoveTimeout = 3 * time.Second
	ts, h := newTestServer(t, cfg)
	a, b, wa, _, _, _ := startMatchGame(t, ts, "alice", "bob")
	defer b.Close()

	a.Close()
	if _, err := b.WaitType("opponent_presence", 3*time.Second); err != nil {
		t.Fatal(err)
	}

	over, err := b.WaitType("game_over", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if over["reason"] != "disconnect_timeout" || over["winner"] != "white" {
		t.Fatalf("bad disconnect_timeout game_over: %v", over)
	}

	// A resumes afterwards: session lives, but the game is gone -> lobby.
	a2 := dial(t, ts)
	defer a2.Close()
	if err := a2.SendMap(map[string]any{"type": "hello", "nick": "alice", "token": wa["token"]}); err != nil {
		t.Fatal(err)
	}
	w, err := a2.WaitType("welcome", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if w["resumed"] != true {
		t.Fatalf("should resume session: %v", w)
	}
	// No game_state may follow. Drain briefly and assert none arrived.
	for _, m := range a2.Collect(300 * time.Millisecond) {
		if mType(m) == "game_state" {
			t.Fatalf("dead game must not produce game_state: %v", m)
		}
	}

	// The resumed player is back in the lobby and can act normally.
	if err := a2.SendMap(map[string]any{"type": "create_room"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a2.WaitType("room", 3*time.Second); err != nil {
		t.Fatal("resumed player cannot create a room")
	}

	if h.Metrics().GamesPlayed != 1 {
		t.Fatalf("gamesPlayed = %d, want 1", h.Metrics().GamesPlayed)
	}
}

// --------------------------------------------------------------- heartbeat

func TestWSPongTimeout(t *testing.T) {
	cfg := fastCfg()
	cfg.WSPingPeriod = 40 * time.Millisecond
	cfg.WSPongWait = 120 * time.Millisecond
	ts, _ := newTestServer(t, cfg)
	c, err := wsclient.DialOpts(ts.URL+"/ws", nil, true) // ignore ws pings -> server must drop us
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	hello(t, c, "alice")

	if _, err := c.Read(2 * time.Second); err == nil {
		t.Fatal("server should close a silent-pong connection")
	}
}

// --------------------------------------------------------------- protocol

func TestUnknownTypeIgnored(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())
	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "alice")

	if err := c.SendMap(map[string]any{"type": "bogus", "foo": 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMap(map[string]any{"type": "list_rooms"}); err != nil {
		t.Fatal(err)
	}
	m, err := c.Read(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The first frame after a bogus type must be the list answer — no error.
	if mType(m) == "error" {
		t.Fatalf("unknown type must be ignored, got error: %v", m)
	}
	if mType(m) != "room_list" {
		t.Fatalf("expected room_list, got %v", m)
	}
}

// ------------------------------------------------------------ HTTP & misc

func TestHTTPEndpoints(t *testing.T) {
	ts, _ := newTestServer(t, fastCfg())

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz status %d", resp.StatusCode)
	}
	buf := make([]byte, 8)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "ok" {
		t.Fatalf("healthz body %q", buf[:n])
	}

	resp2, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"connections", "lobbyPlayers", "rooms", "activeGames", "gamesPlayed"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("metrics missing %q: %v", k, m)
		}
	}
}

// ---------------------------------------------------------- 100-room gate

func TestConcurrent100Rooms(t *testing.T) {
	cfg := fastCfg()
	cfg.MoveTimeout = 10 * time.Second
	cfg.ReconnectWindow = 10 * time.Second
	ts, h := newTestServer(t, cfg)

	const pairs = 100
	var wg sync.WaitGroup
	errCh := make(chan error, pairs*2)
	overCh := make(chan map[string]any, pairs*2)

	players := make([]*wsclient.Client, 0, pairs*2)
	for i := 0; i < pairs*2; i++ {
		c := dial(t, ts)
		if _, err := c.Hello(fmt.Sprintf("p%d", i), ""); err != nil {
			t.Fatalf("hello %d: %v", i, err)
		}
		players = append(players, c)
	}
	// All 200 matchmakes fire at once: every pairing bursts room_list
	// pushes at every lobby conn. The per-conn merge (R3) keeps only the
	// newest snapshot, so no healthy client trips the slow-consumer kick.
	type starter struct {
		c     *wsclient.Client
		first map[string]any
	}
	for _, c := range players {
		if err := c.SendMap(map[string]any{"type": "matchmake"}); err != nil {
			t.Fatal(err)
		}
	}
	starters := make([]starter, 0, pairs*2)
	for _, c := range players {
		gs, err := c.WaitType("game_start", 30*time.Second)
		if err != nil {
			t.Fatalf("game_start: %v", err)
		}
		starters = append(starters, starter{c, gs})
	}

	started := 0
	for _, s := range starters {
		started++
		wg.Add(1)
		go func(c *wsclient.Client, first map[string]any) {
			defer wg.Done()
			defer c.Close()
			over, err := playScriptedWin(c, first["youColor"].(string), first)
			if err != nil {
				errCh <- err
				return
			}
			overCh <- over
		}(s.c, s.first)
	}
	if started != pairs*2 {
		t.Fatalf("players started = %d, want %d", started, pairs*2)
	}
	wg.Wait()
	close(overCh)
	close(errCh)
	for err := range errCh {
		t.Errorf("player error: %v", err)
	}
	overs := 0
	for m := range overCh {
		overs++
		if m["reason"] != "five" || m["winner"] != "black" {
			t.Errorf("bad game_over: %v", m)
		}
	}
	if overs != pairs*2 {
		t.Fatalf("game_over count = %d, want %d", overs, pairs*2)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.Metrics().GamesPlayed == pairs {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("gamesPlayed = %d, want %d", h.Metrics().GamesPlayed, pairs)
}
