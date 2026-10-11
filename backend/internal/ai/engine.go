// Package ai is the embedded gomoku opponent: a pattern-evaluated
// alpha-beta engine (internal/ai/engine.go) plus the driver goroutine that
// feeds its moves back into the game actor's mailbox (driver.go). The AI
// never bypasses the actor — it posts Move events exactly like a human
// client (DESIGN §4, PROTOCOL §8).
package ai

import (
	"math/rand"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// Config is one difficulty preset. Think caps a single move's thinking
// time; deadline truncation always returns the best move found so far.
type Config struct {
	MaxDepth  int           // alpha-beta depth (Iterative deepens to this)
	Breadth   int           // candidate moves kept per node after ordering
	NoisePct  int           // leaf-eval jitter in percent (easy only)
	Think     time.Duration // per-move budget
	Iterative bool          // iterative deepening until Think expires
}

// For returns the preset for a PROTOCOL §2 difficulty string.
func For(difficulty string) Config {
	switch difficulty {
	case "easy":
		return Config{MaxDepth: 2, Breadth: 6, NoisePct: 40, Think: 400 * time.Millisecond}
	case "normal":
		return Config{MaxDepth: 4, Breadth: 10, Think: 1200 * time.Millisecond}
	case "hard":
		return Config{MaxDepth: 64, Breadth: 14, Iterative: true, Think: 1800 * time.Millisecond}
	}
	return Config{}
}

// Engine searches one board position.
type Engine struct {
	cfg  Config
	rnd  *rand.Rand
	dead time.Time
	stop bool
}

// NewEngine builds an engine from a difficulty preset.
func NewEngine(cfg Config) *Engine {
	return &Engine{cfg: cfg, rnd: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

const winScore = 1 << 24

// pattern weights indexed by stone count inside a five-cell window.
var patScore = [6]int{0, 8, 90, 1100, 16000, winScore}

// Move picks the best placement for color me on b before deadline. ok is
// false only when no legal cell exists. A forced win is played, a forced
// opponent win is blocked — at every difficulty, before any search runs.
func (e *Engine) Move(b *game.Board, me game.Color, deadline time.Time) (x, y int, ok bool) {
	if b == nil {
		return 0, 0, false
	}
	e.dead, e.stop = deadline, false
	think := time.Until(deadline)
	if think > e.cfg.Think {
		e.dead = time.Now().Add(e.cfg.Think)
	}
	if mv, found := e.winInOne(b, me); found {
		return mv[0], mv[1], true
	}
	if mv, found := e.winInOne(b, me.Other()); found {
		return mv[0], mv[1], true
	}
	cands := e.candidates(b, me)
	if len(cands) == 0 {
		// Empty board: open at the center.
		if b.CanPlace(game.BoardSize/2, game.BoardSize/2) {
			return game.BoardSize / 2, game.BoardSize / 2, true
		}
		for yy := 0; yy < game.BoardSize; yy++ {
			for xx := 0; xx < game.BoardSize; xx++ {
				if b.CanPlace(xx, yy) {
					return xx, yy, true
				}
			}
		}
		return 0, 0, false
	}

	maxDepth := e.cfg.MaxDepth
	if !e.cfg.Iterative {
		return e.rootSearch(b, me, cands, maxDepth)
	}
	// Iterative deepening: deepen until the deadline, keep the last
	// completed iteration's answer.
	best := cands[0]
	for d := 1; d <= maxDepth; d++ {
		mx, my, mok := e.rootSearch(b, me, cands, d)
		if e.stop {
			return best[0], best[1], true // truncated mid-iteration
		}
		if mok {
			best = [2]int{mx, my}
		}
		if e.expired() {
			break
		}
	}
	return best[0], best[1], true
}

func (e *Engine) expired() bool {
	return e.stop || time.Now().After(e.dead)
}

// rootSearch runs one full-depth alpha-beta iteration and returns the best
// move; ok=false when the deadline aborted the iteration (caller keeps the
// previous answer).
func (e *Engine) rootSearch(b *game.Board, me game.Color, cands [][2]int, depth int) (x, y int, ok bool) {
	bestScore := -winScore * 4
	best := cands[0]
	for _, p := range cands {
		b.Place(p[0], p[1], me)
		var s int
		if b.WinFrom(p[0], p[1]) {
			s = winScore
		} else {
			s = -e.search(b, me.Other(), depth-1, -winScore*4, -bestScore, 1)
		}
		b[p[1]][p[0]] = game.Empty
		if e.stop {
			return best[0], best[1], false
		}
		if e.cfg.NoisePct > 0 && depth == e.cfg.MaxDepth {
			s += e.rnd.Intn(max(1, s*e.cfg.NoisePct/100) + 1)
		}
		if s > bestScore {
			bestScore, best = s, p
		}
	}
	return best[0], best[1], true
}

// search is plain negamax alpha-beta scoring from the side-to-move's view.
func (e *Engine) search(b *game.Board, me game.Color, depth, alpha, beta, ply int) int {
	if depth <= 0 || e.expired() {
		return e.evalFor(b, me)
	}
	cands := e.candidates(b, me)
	if len(cands) == 0 {
		return 0
	}
	best := -winScore * 4
	for i, p := range cands {
		if i >= e.cfg.Breadth {
			break
		}
		b.Place(p[0], p[1], me)
		var s int
		if b.WinFrom(p[0], p[1]) {
			s = winScore - ply
		} else if b.Full() {
			s = 0
		} else {
			s = -e.search(b, me.Other(), depth-1, -beta, -alpha, ply+1)
		}
		b[p[1]][p[0]] = game.Empty
		if e.stop {
			return best
		}
		if s > best {
			best = s
		}
		if s > alpha {
			alpha = s
		}
		if alpha >= beta {
			break
		}
	}
	return best
}

// winInOne returns a cell that completes five for color c, if any.
func (e *Engine) winInOne(b *game.Board, c game.Color) ([2]int, bool) {
	for _, p := range e.candidates(b, c) {
		b.Place(p[0], p[1], c)
		won := b.WinFrom(p[0], p[1])
		b[p[1]][p[0]] = game.Empty
		if won {
			return p, true
		}
	}
	return [2]int{}, false
}

type scored struct {
	p [2]int
	s int
}

// candidates enumerates empty cells within Chebyshev distance 2 of any
// stone, ordered by a static point heuristic (attack + weighted defense).
func (e *Engine) candidates(b *game.Board, me game.Color) [][2]int {
	var out []scored
	seen := false
	for yy := 0; yy < game.BoardSize; yy++ {
		for xx := 0; xx < game.BoardSize; xx++ {
			if b[yy][xx] != game.Empty {
				seen = true
				continue
			}
			if !e.nearStone(b, xx, yy) {
				continue
			}
			s := e.pointScore(b, xx, yy, me) + e.pointScore(b, xx, yy, me.Other())*11/10 + centerBias(xx, yy)
			out = append(out, scored{[2]int{xx, yy}, s})
		}
	}
	if !seen {
		return nil
	}
	// Insertion-sort top breadth only; candidate lists stay small.
	sortDesc(out)
	if len(out) > e.cfg.Breadth*2 {
		out = out[:e.cfg.Breadth*2]
	}
	res := make([][2]int, len(out))
	for i, s := range out {
		res[i] = s.p
	}
	return res
}

func sortDesc(a []scored) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].s > a[j-1].s; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func (e *Engine) nearStone(b *game.Board, x, y int) bool {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			if game.InBounds(nx, ny) && b[ny][nx] != game.Empty {
				return true
			}
		}
	}
	return false
}

// pointScore rates placing color c at empty (x,y): the longest run it
// completes through that cell on each axis, summed over all four.
func (e *Engine) pointScore(b *game.Board, x, y int, c game.Color) int {
	total := 0
	for _, d := range [4][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}} {
		n := 1 + runLen(b, x, y, d[0], d[1], c) + runLen(b, x, y, -d[0], -d[1], c)
		if n >= 5 {
			return winScore
		}
		total += patScore[n]
	}
	return total
}

func runLen(b *game.Board, x, y, dx, dy int, c game.Color) int {
	n := 0
	for nx, ny := x+dx, y+dy; game.InBounds(nx, ny) && b[ny][nx] == c; nx, ny = nx+dx, ny+dy {
		n++
	}
	return n
}

// evalFor scores the whole board from me's perspective: my pattern sum minus
// the opponent's (defense weighted slightly higher so threats read worse).
func (e *Engine) evalFor(b *game.Board, me game.Color) int {
	return e.boardScore(b, me) - e.boardScore(b, me.Other())*12/10
}

// boardScore sums five-cell window patterns for one color over all axes.
func (e *Engine) boardScore(b *game.Board, c game.Color) int {
	total := 0
	dirs := [4][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		for yy := 0; yy < game.BoardSize; yy++ {
			for xx := 0; xx < game.BoardSize; xx++ {
				// Only windows whose first cell is (xx,yy) as the line head.
				px, py := xx-d[0], yy-d[1]
				if game.InBounds(px, py) {
					continue // start each line once
				}
				total += e.lineScore(b, xx, yy, d[0], d[1], c)
			}
		}
	}
	return total
}

// lineScore walks one full line starting at (x,y) and slides 5-windows.
func (e *Engine) lineScore(b *game.Board, x, y, dx, dy int, c game.Color) int {
	line := [game.BoardSize]game.Color{}
	n := 0
	for cx, cy := x, y; game.InBounds(cx, cy); cx, cy = cx+dx, cy+dy {
		line[n] = b[cy][cx]
		n++
	}
	total := 0
	for i := 0; i+5 <= n; i++ {
		mine, opp := 0, 0
		for k := 0; k < 5; k++ {
			if line[i+k] == c {
				mine++
			} else if line[i+k] != game.Empty {
				opp++
			}
		}
		if mine > 0 && opp == 0 {
			total += patScore[mine]
		}
	}
	return total
}

func centerBias(x, y int) int {
	mid := game.BoardSize / 2
	d := abs(x-mid) + abs(y-mid)
	return 14 - d
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
