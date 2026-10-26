package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
	"github.com/Aqua196/gomoku-battle/backend/internal/ws"
	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
	"github.com/gorilla/websocket"
)

// A burst past the bucket is dropped with one rate_limited error; the
// connection survives and serves again once tokens refill.
func TestRateLimitDropsFlood(t *testing.T) {
	cfg := fastCfg()
	cfg.MsgRate, cfg.MsgBurst = 10, 5
	ts, _ := newTestServer(t, cfg)
	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "flood") // hello spends one token

	for i := 0; i < 30; i++ {
		if err := c.SendMap(map[string]any{"type": "ping"}); err != nil {
			t.Fatal(err)
		}
	}
	pongs, limited := 0, 0
	for _, m := range c.Collect(400 * time.Millisecond) {
		switch {
		case mType(m) == "pong":
			pongs++
		case mErrCode(m) == "rate_limited":
			limited++
		}
	}
	if limited != 1 {
		t.Fatalf("want exactly one rate_limited error per second, got %d", limited)
	}
	if pongs < 4 || pongs > 12 {
		t.Fatalf("pongs = %d, want roughly the burst (4 left) plus refill", pongs)
	}

	time.Sleep(600 * time.Millisecond) // refill
	if err := c.SendMap(map[string]any{"type": "list_rooms"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitType("room_list", 2*time.Second); err != nil {
		t.Fatalf("connection should serve again after refill: %v", err)
	}
}

// The per-IP cap refuses the upgrade with 429 and frees the slot on close.
func TestMaxConnsPerIP(t *testing.T) {
	cfg := fastCfg()
	cfg.MaxConnsPerIP = 2
	ts, _ := newTestServer(t, cfg)
	a, b := dial(t, ts), dial(t, ts)
	defer b.Close()

	_, err := wsclient.Dial(ts.URL+"/ws", nil)
	if !errors.Is(err, websocket.ErrBadHandshake) {
		t.Fatalf("third connection from one address: want bad handshake (429), got %v", err)
	}

	a.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := wsclient.Dial(ts.URL+"/ws", nil)
		if err == nil {
			c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not released after close: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Shutdown closes every websocket with 1001 and refuses new upgrades.
func TestShutdownGoesAway(t *testing.T) {
	cfg := fastCfg()
	h := hub.New(store.NewMemStore(), cfg)
	srv := ws.NewServer(h, cfg, true)
	ts := httptest.NewServer(srv.Handler("", false))
	defer ts.Close()

	c := dial(t, ts)
	defer c.Close()
	hello(t, c, "alice")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_, err := c.Read(2 * time.Second)
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("want close 1001, got %v", err)
	}

	resp, err := http.Get(ts.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upgrade during shutdown: status %d, want 503", resp.StatusCode)
	}
	if n := h.Metrics().Connections; n != 0 {
		t.Fatalf("connections after drain = %d", n)
	}
}
