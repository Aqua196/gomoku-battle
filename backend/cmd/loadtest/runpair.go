package main

import (
	"math/rand"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

// frame is the compacted subset of a wire frame pairRun needs. Buffering
// whole game_state maps (~5KB each) per conn OOMs the driver at 500+
// rooms, so the forwarder keeps only routing fields.
type frame struct {
	typ  string // message type
	code string // error code, for type=="error"
	lx   int
	ly   int
	hasL bool   // game.lastMove present
	yc   string // youColor, for type=="game_start"
	turn string // game.turn, for type=="game_state"
}

// pairRun alternates real moves between the two conns of a room (or the
// human conn in ai mode) until game_over or the shared deadline.
//
// Latency accounting: the accept of my move is the first game_state whose
// game.lastMove equals my (x,y) — stale broadcasts of older moves carry a
// different lastMove and are skipped.
func pairRun(a, b *wsclient.Client, deadline time.Time, thinkBase time.Duration, lat *latency, errc *errorCounter) {
	frames := map[*wsclient.Client]chan frame{}
	for _, c := range []*wsclient.Client{a, b} {
		if c == nil {
			continue
		}
		ch := make(chan frame, 256)
		frames[c] = ch
		go func(c *wsclient.Client) {
			defer close(ch)
			for {
				m, err := c.Read(90 * time.Second)
				if err != nil {
					return
				}
				var fr frame
				fr.typ, _ = m["type"].(string)
				fr.code, _ = m["code"].(string)
				fr.yc, _ = m["youColor"].(string)
				if g, _ := m["game"].(map[string]any); g != nil {
					fr.turn, _ = g["turn"].(string)
					if lm, _ := g["lastMove"].(map[string]any); lm != nil {
						fr.lx = int(dbl(lm["x"]))
						fr.ly = int(dbl(lm["y"]))
						fr.hasL = true
					}
				}
				select {
				case ch <- fr:
				default:
					<-ch     // evict oldest
					ch <- fr // newest always lands
				}
			}
		}(c)
	}

	mover, other := a, b
	for _, c := range []*wsclient.Client{a, b} {
		if c == nil {
			continue
		}
		f, ok := waitFor(frames[c], "game_start", deadline)
		if !ok {
			return
		}
		if f.yc == "black" {
			mover = c
			if c == a {
				other = b
			} else {
				other = a
			}
		}
	}

	for time.Now().Before(deadline) {
	retry:
		x, y := rand.Intn(15), rand.Intn(15)
		t0 := time.Now()
		if err := mover.SendMap(map[string]any{"type": "move", "x": x, "y": y}); err != nil {
			errc.add("send_move")
			return
		}
		for time.Now().Before(deadline) {
			f, ok := nextFrame(frames[mover], deadline)
			if !ok {
				return
			}
			switch f.typ {
			case "game_state":
				if f.hasL && f.lx == x && f.ly == y {
					lat.add(time.Since(t0))
					goto accepted
				}
			case "game_over":
				return
			case "error":
				errc.add(f.code)
				if f.code == "illegal_move" {
					goto retry // occupied cell: resend on the same turn
				}
				return
			}
		}
		return
	accepted:
		if other != nil {
			mover, other = other, mover
		} else {
			// ai mode: the AI's reply is a game_state with turn back to
			// us (black) — wait for it before sending the next move.
			for time.Now().Before(deadline) {
				f, ok := nextFrame(frames[mover], deadline)
				if !ok {
					return
				}
				if f.typ == "game_over" {
					return
				}
				if f.typ == "game_state" && f.turn == "black" {
					break
				}
			}
			if time.Now().After(deadline) {
				return
			}
		}
		if thinkBase > 0 {
			time.Sleep(thinkBase/2 + time.Duration(rand.Int63n(int64(thinkBase))))
		}
	}
}

func dbl(v any) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return 0
}

func waitFor(ch chan frame, want string, deadline time.Time) (frame, bool) {
	for time.Now().Before(deadline) {
		f, ok := nextFrame(ch, deadline)
		if !ok {
			return frame{}, false
		}
		if f.typ == want {
			return f, true
		}
	}
	return frame{}, false
}

func nextFrame(ch chan frame, deadline time.Time) (frame, bool) {
	select {
	case f, ok := <-ch:
		return f, ok
	case <-time.After(time.Until(deadline)):
		return frame{}, false
	}
}
