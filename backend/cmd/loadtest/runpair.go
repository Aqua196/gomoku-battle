package main

import (
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

// frame is the compacted subset of a wire frame the driver needs. Buffering
// whole game_state maps (~5KB each) per conn OOMs the driver at 500+
// rooms, so the forwarder keeps only routing fields.
type frame struct {
	typ    string // message type
	code   string // error code, for type=="error"
	action string // room action, for type=="room"
	roomID string // room.id, for type=="room" action=="created"
	lx     int
	ly     int
	hasL   bool   // game.lastMove present
	yc     string // youColor, for type=="game_start"
	turn   string // game.turn, for type=="game_state"
}

// forwarder runs for the whole life of one conn (one per conn, started by
// the pair's owner goroutine). It exits on done, on conn error, or when the
// underlying wsclient.Read fails. Frames are compacted and pushed into ch;
// a full ch evicts the OLDEST frame and counts the eviction — dropping a
// frame hides the slowest samples otherwise (M3-R4 survivor bias).
func forwarder(c *wsclient.Client, ch chan frame, done chan struct{}, errc *errorCounter) {
	defer close(ch)
	for {
		m, err := c.Read(90 * time.Second)
		if err != nil {
			select {
			case <-done:
				return // teardown: conn was closed by us, not lost
			default:
			}
			errc.add("conn_dead")
			select {
			case ch <- frame{typ: "_dead"}:
			case <-done:
			}
			return
		}
		var f frame
		f.typ, _ = m["type"].(string)
		f.code, _ = m["code"].(string)
		f.action, _ = m["action"].(string)
		f.yc, _ = m["youColor"].(string)
		if r, _ := m["room"].(map[string]any); r != nil {
			f.roomID, _ = r["id"].(string)
		}
		if g, _ := m["game"].(map[string]any); g != nil {
			f.turn, _ = g["turn"].(string)
			if lm, _ := g["lastMove"].(map[string]any); lm != nil {
				f.lx = int(dbl(lm["x"]))
				f.ly = int(dbl(lm["y"]))
				f.hasL = true
			}
		}
		select {
		case <-done:
			return
		case ch <- f:
		default:
			select {
			case <-ch:
				errc.add("evicted") // dropped frame: counted, never hidden
			case <-done:
				return
			}
			select {
			case ch <- f:
			case <-done:
				return
			}
		}
	}
}

// pairRun plays ONE room to game_over between a and b (or the human conn in
// ai mode). It returns true when a real game_over frame was observed.
//
// Latency accounting: my move is "accepted" by the first game_state whose
// lastMove==(x,y) AND whose turn flipped to the opponent's color — both
// conditions are required so a stale broadcast carrying the same cell
// cannot forge a sample (M3-R3). A local occupancy board prevents sending
// stones onto taken cells entirely.
func pairRun(a, b *wsclient.Client, fa, fb chan frame, timer <-chan time.Time, thinkBase time.Duration, lat *latency, sent, accepted *int64, errc *errorCounter) bool {
	frames := map[*wsclient.Client]chan frame{a: fa, b: fb}
	myc := map[*wsclient.Client]string{a: "", b: ""}
	mover, other := a, b
	for _, c := range []*wsclient.Client{a, b} {
		if c == nil {
			continue
		}
		f, ok := waitFor(frames[c], "game_start", timer)
		if !ok {
			return false
		}
		myc[c] = f.yc
		if f.yc == "black" {
			mover = c
			if c == a {
				other = b
			} else {
				other = a
			}
		}
	}
	oppColor := map[*wsclient.Client]string{}
	if other != nil {
		oppColor[mover], oppColor[other] = "white", "black"
		if myc[mover] == "white" {
			oppColor[mover], oppColor[other] = "black", "white"
		}
	}

	var occupied [15][15]bool
	free := 225
	for {
		select {
		case <-timer:
			return false
		default:
		}
		// Local occupancy: only empty cells are candidates (no illegal
		// retry flood, M3-R3).
		var x, y int
		if free <= 0 {
			return false
		}
		for {
			x, y = rand.Intn(15), rand.Intn(15)
			if !occupied[x][y] {
				break
			}
		}
		t0 := time.Now()
		if err := mover.SendMap(map[string]any{"type": "move", "x": x, "y": y}); err != nil {
			errc.add("send_move")
			return false
		}
		atomic.AddInt64(sent, 1)
		for {
			f, ok := nextFrame(frames[mover], timer)
			if !ok {
				return false
			}
			switch f.typ {
			case "game_state":
				if f.hasL {
					occupied[f.lx][f.ly] = true
					free--
				}
				// Accept iff lastMove is mine AND the turn flipped to the
				// opponent — rules out stale broadcasts (M3-R3).
				if f.hasL && f.lx == x && f.ly == y && f.turn == oppColor[mover] {
					lat.add(time.Since(t0))
					atomic.AddInt64(accepted, 1)
					goto accepted
				}
			case "game_over":
				return true
			case "error":
				if f.code == "not_your_turn" {
					errc.add("desync:not_your_turn") // tool-side desync, never expected
					return false
				}
				errc.add(f.code)
				if f.code != "illegal_move" {
					return false
				}
				// illegal_move while our board thought the cell free:
				// the local board drifted — count it and resync on the
				// next game_state rather than resending blindly.
			}
		}
	accepted:
		if other != nil {
			mover, other = other, mover
		} else {
			// ai mode: wait for the AI's reply to flip the turn back to
			// black (the human) before sending again.
			for {
				f, ok := nextFrame(frames[mover], timer)
				if !ok {
					return false
				}
				if f.typ == "game_over" {
					return true
				}
				if f.typ == "game_state" {
					if f.hasL {
						if !occupied[f.lx][f.ly] {
							occupied[f.lx][f.ly] = true
							free--
						}
					}
					if f.turn == "black" {
						break
					}
				}
			}
		}
		if thinkBase > 0 {
			select {
			case <-timer:
				return false
			case <-time.After(thinkBase/2 + time.Duration(rand.Int63n(int64(thinkBase)))):
			}
		}
	}
}

func dbl(v any) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return 0
}

// waitFor drains ch until a frame of the wanted type arrives (other frames
// are consumed and ignored) or the shared deadline timer fires.
func waitFor(ch chan frame, want string, timer <-chan time.Time) (frame, bool) {
	for {
		f, ok := nextFrame(ch, timer)
		if !ok {
			return frame{}, false
		}
		if f.typ == want {
			return f, true
		}
	}
}

// nextFrame reuses the caller's timer — no per-call allocation (M3-R11).
func nextFrame(ch chan frame, timer <-chan time.Time) (frame, bool) {
	select {
	case f, ok := <-ch:
		if f.typ == "_dead" {
			return f, false // counted upstream; same exit path as the deadline
		}
		return f, ok
	case <-timer:
		return frame{}, false
	}
}
