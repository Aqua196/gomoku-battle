package integration

// Container/外部实例关卡测试：设置 WS_ADDR 时对运行中的服务端（如
// docker compose 起的 app 容器）跑全流程集成路径——hello→matchmake→
// 整局分胜负→断线重连恢复→人机对局。不设置时 skip（CI 里由 compose
// 关卡步骤启用）。复用与 httptest 套件相同的 wsclient 代码路径。

import (
	"os"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

func wsAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("WS_ADDR")
	if addr == "" {
		t.Skip("WS_ADDR not set — container gate probe only")
	}
	return addr
}

func dialExt(t *testing.T, addr string) *wsclient.Client {
	t.Helper()
	c, err := wsclient.Dial(addr, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	return c
}

func TestContainerMatchGameReconnectAI(t *testing.T) {
	addr := wsAddr(t)

	// --- matchmake pair → full game, black wins on column 7
	a := dialExt(t, addr)
	defer a.Close()
	b := dialExt(t, addr)
	defer b.Close()
	if _, err := a.Hello("GateAlice", ""); err != nil {
		t.Fatalf("hello a: %v", err)
	}
	if _, err := b.Hello("GateBob", ""); err != nil {
		t.Fatalf("hello b: %v", err)
	}
	must := func(c *wsclient.Client, m map[string]any) {
		t.Helper()
		if err := c.SendMapTimeout(m, 5*time.Second); err != nil {
			t.Fatalf("send %v: %v", m["type"], err)
		}
	}
	must(a, map[string]any{"type": "matchmake"})
	must(b, map[string]any{"type": "matchmake"})
	ga, err := a.WaitType("game_start", 8*time.Second)
	if err != nil {
		t.Fatalf("game_start a: %v", err)
	}
	if _, err := b.WaitType("game_start", 8*time.Second); err != nil {
		t.Fatalf("game_start b: %v", err)
	}
	black, white := a, b
	if ga["youColor"] != "black" {
		black, white = b, a
	}
	seq := [][2]int{{7, 7}, {3, 3}, {7, 8}, {4, 4}, {7, 9}, {5, 5}, {7, 10}, {6, 6}, {7, 11}}
	var over map[string]any
	for i, m := range seq {
		who := black
		if i%2 == 1 {
			who = white
		}
		must(who, map[string]any{"type": "move", "x": m[0], "y": m[1]})
		// The winning move ends the game: server sends game_over instead of a
		// final game_state (actor.finish before broadcast). Accept either.
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) && over == nil {
			fr, err := who.Read(time.Until(deadline))
			if err != nil {
				t.Fatalf("read after move %d: %v", i, err)
			}
			switch fr["type"] {
			case "error":
				t.Fatalf("server error frame after move %d: %v", i, fr)
			case "game_over":
				over = fr
			case "game_state":
				if mn, _ := fr["game"].(map[string]any)["moveNumber"].(float64); int(mn) == i+1 {
					goto nextMove
				}
			}
		}
		if over == nil {
			t.Fatalf("timeout after move %d waiting game_state/game_over", i)
		}
	nextMove:
	}
	if over == nil {
		over, err = black.WaitType("game_over", 8*time.Second)
		if err != nil {
			t.Fatalf("game_over: %v", err)
		}
	}
	if over["winner"] != "black" || over["reason"] != "five" {
		t.Fatalf("expected black five win, got %v", over)
	}
	t.Log("match→game→five win: PASS")

	// --- disconnect + token reconnect restores game
	c := dialExt(t, addr)
	defer c.Close()
	d := dialExt(t, addr)
	defer d.Close()
	wc, err := c.Hello("GateC", "")
	if err != nil {
		t.Fatalf("hello c: %v", err)
	}
	wd, err := d.Hello("GateD", "")
	if err != nil {
		t.Fatalf("hello d: %v", err)
	}
	must(c, map[string]any{"type": "matchmake"})
	must(d, map[string]any{"type": "matchmake"})
	gc, err := c.WaitType("game_start", 8*time.Second)
	if err != nil {
		t.Fatalf("game_start c: %v", err)
	}
	if _, err := d.WaitType("game_start", 8*time.Second); err != nil {
		t.Fatalf("game_start d: %v", err)
	}
	mover := c
	moverNick, moverTok := "GateC", wc["token"].(string)
	if gc["youColor"] != "black" {
		mover, moverNick, moverTok = d, "GateD", wd["token"].(string)
	}
	must(mover, map[string]any{"type": "move", "x": 9, "y": 9})
	if _, err := mover.WaitType("game_state", 8*time.Second); err != nil {
		t.Fatalf("gs after move: %v", err)
	}
	mover.Close()
	rec := dialExt(t, addr)
	defer rec.Close()
	wr, err := rec.Hello(moverNick, moverTok)
	if err != nil {
		t.Fatalf("re-hello: %v", err)
	}
	if wr["resumed"] != true {
		t.Fatalf("resumed != true: %v", wr)
	}
	st, err := rec.WaitType("game_state", 8*time.Second)
	if err != nil {
		t.Fatalf("restored game_state: %v", err)
	}
	if mn, _ := st["game"].(map[string]any)["moveNumber"].(float64); mn < 1 {
		t.Fatalf("restored board empty: %v", st)
	}
	t.Log("disconnect→token reconnect restore: PASS")

	// --- AI game through the same wire
	e := dialExt(t, addr)
	defer e.Close()
	if _, err := e.Hello("GateAI", ""); err != nil {
		t.Fatalf("hello e: %v", err)
	}
	must(e, map[string]any{"type": "play_ai", "difficulty": "normal"})
	ge, err := e.WaitType("game_start", 8*time.Second)
	if err != nil {
		t.Fatalf("game_start ai: %v", err)
	}
	g := ge["game"].(map[string]any)
	if pl := g["players"].(map[string]any)["white"].(map[string]any); pl["isAI"] != true {
		t.Fatalf("white seat not AI: %v", pl)
	}
	must(e, map[string]any{"type": "move", "x": 7, "y": 7})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m, err := e.Read(time.Until(deadline))
		if err != nil {
			t.Fatalf("read waiting AI reply: %v", err)
		}
		if m["type"] == "error" {
			t.Fatalf("server error frame: %v", m)
		}
		if m["type"] != "game_state" {
			continue
		}
		if mn, _ := m["game"].(map[string]any)["moveNumber"].(float64); int(mn) >= 2 {
			t.Log("play_ai full round-trip: PASS")
			return
		}
	}
	t.Fatal("timeout waiting for AI reply (moveNumber>=2)")
}
