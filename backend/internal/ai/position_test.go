package ai

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// The incremental board must always equal one rebuilt from scratch: same
// key, same line scores and threat masks, same neighbourhood counts.
func TestPositionIncrementalMatchesRebuild(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var p position
	var b game.Board
	var placed []move
	for step := 0; step < 3000; step++ {
		if len(placed) > 0 && (r.IntN(3) == 0 || len(placed) > 150) {
			i := r.IntN(len(placed))
			m := placed[i]
			placed = append(placed[:i], placed[i+1:]...)
			p.remove(m.x, m.y)
			b[m.y][m.x] = game.Empty
		} else {
			x, y := r.IntN(size), r.IntN(size)
			if b[y][x] != game.Empty {
				continue
			}
			c := game.Color(1 + r.IntN(2))
			p.place(x, y, c)
			b[y][x] = c
			placed = append(placed, move{x, y})
		}
		var fresh position
		fresh.load(&b)
		if fresh != p {
			t.Fatalf("step %d: incremental position diverged from rebuild", step)
		}
	}
}

func TestScanLineShapes(t *testing.T) {
	const X, O, E = game.Black, game.White, game.Empty
	cases := []struct {
		name       string
		line       []game.Color
		five, four int // popcounts of the masks for X
		openThree  bool
		openFour   bool
	}{
		{"open three", []game.Color{E, E, X, X, X, E, E}, 0, 4, true, false},
		{"split three", []game.Color{E, X, E, X, X, E, E}, 0, 3, true, false},
		{"blocked three", []game.Color{O, X, X, X, E, E, E}, 0, 2, false, false},
		{"open four", []game.Color{E, X, X, X, X, E, E}, 2, 2, false, true},
		{"rush four", []game.Color{O, X, X, X, X, E, E}, 1, 2, false, false},
		{"broken four", []game.Color{O, X, X, E, X, X, O}, 1, 0, false, false},
		{"dead (no room)", []game.Color{O, X, X, X, X, O}, 0, 0, false, false},
	}
	for _, c := range cases {
		score, five, four := scanLine(c.line, X)
		if got := popcount(five); got != c.five {
			t.Errorf("%s: five cells %d, want %d", c.name, got, c.five)
		}
		if got := popcount(four); got != c.four {
			t.Errorf("%s: four makers %d, want %d", c.name, got, c.four)
		}
		if c.openFour && score < openFourBonus {
			t.Errorf("%s: score %d lacks the open-four bonus", c.name, score)
		}
		if c.openThree {
			// Block the cell before the first stone: the shape stops being open.
			closed := slices.Clone(c.line)
			closed[slices.Index(closed, X)-1] = O
			if blocked, _, _ := scanLine(closed, X); score-blocked < openThreeBonus {
				t.Errorf("%s: open %d vs closed %d lacks the open-three bonus", c.name, score, blocked)
			}
		}
	}
}

func popcount(m uint16) int {
	n := 0
	for ; m != 0; m &= m - 1 {
		n++
	}
	return n
}

// A double four is the shortest VCF: one move, two completion cells.
func TestVCFDoubleFour(t *testing.T) {
	var b game.Board
	for _, s := range [][2]int{{4, 7}, {5, 7}, {6, 7}, {7, 4}, {7, 5}, {7, 6}} {
		place(&b, s[0], s[1], game.White)
	}
	for _, s := range [][2]int{{3, 7}, {7, 3}, {0, 0}, {14, 14}, {0, 14}, {14, 0}, {12, 1}} {
		place(&b, s[0], s[1], game.Black)
	}
	e := engine("hard", 300*time.Millisecond)
	x, y, ok := e.Move(&b, game.White, time.Now().Add(time.Second))
	if !ok || x != 7 || y != 7 {
		t.Fatalf("move (%d,%d) ok=%v, want the double-four point (7,7)", x, y, ok)
	}
	if e.Stats().Reason != "vcf" {
		t.Fatalf("reason %q, want vcf", e.Stats().Reason)
	}
}

// Every VCF the solver reports must actually win when played out against
// the forced defence — checked on many random mid-game positions.
func TestVCFSoundOnRandomPositions(t *testing.T) {
	e := NewEngine(For("hard"))
	e.deadline = time.Now().Add(time.Hour)
	r := rand.New(rand.NewPCG(7, 7))
	found, longest := 0, 0
	for trial := 0; trial < 400; trial++ {
		var b game.Board
		stones := 20 + r.IntN(40)
		for i := 0; i < stones; i++ {
			x, y := 3+r.IntN(9), 3+r.IntN(9) // dense centre, lots of shapes
			if b[y][x] == game.Empty {
				b[y][x] = game.Color(1 + i%2)
			}
		}
		e.pos.load(&b)
		if e.pos.hasFive(game.Black) || e.pos.hasFive(game.White) || anyFive(&b) {
			continue // already decided: not a VCF question
		}
		clear(e.vcfFail)
		e.stats = Stats{}
		if _, ok := e.solveVCF(game.Black, 12); !ok {
			continue
		}
		found++
		n, ok := playOutVCF(e, game.Black)
		if !ok {
			t.Fatalf("trial %d: reported VCF did not win when played out", trial)
		}
		longest = max(longest, n)
	}
	t.Logf("positions with a VCF: %d, longest combination: %d attacker moves", found, longest)
	if found < 20 || longest < 3 {
		t.Fatalf("sample too weak: found=%d longest=%d", found, longest)
	}
}

// playOutVCF plays the solver's line: attacker's four, defender's only
// block, repeat. It returns the number of attacker moves to five.
func playOutVCF(e *Engine, att game.Color) (int, bool) {
	p := &e.pos
	def := att.Other()
	for n := 1; n <= 30; n++ {
		clear(e.vcfFail)
		e.stats = Stats{}
		m, ok := e.solveVCF(att, 12)
		if !ok {
			return n, false
		}
		p.place(m.x, m.y, att)
		if p.fiveAt(m.x, m.y) {
			return n, true
		}
		wins := p.fiveCells(att, nil)
		switch len(wins) {
		case 0:
			return n, false // not a four: the defender is free
		case 1:
			p.place(wins[0].x, wins[0].y, def)
			if p.fiveAt(wins[0].x, wins[0].y) {
				return n, false
			}
		default:
			return n + 1, true // two completions: block one, five on the other
		}
	}
	return 30, false
}

func anyFive(b *game.Board) bool {
	for y := range size {
		for x := range size {
			if b[y][x] != game.Empty && b.WinFrom(x, y) {
				return true
			}
		}
	}
	return false
}

// When the opponent has a VCF, the engine's move must leave it without one.
func TestVCFDefence(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 3))
	checked := 0
	for trial := 0; trial < 300 && checked < 15; trial++ {
		var b game.Board
		for i := 0; i < 24+r.IntN(20); i++ {
			x, y := 3+r.IntN(9), 3+r.IntN(9)
			if b[y][x] == game.Empty {
				b[y][x] = game.Color(1 + i%2)
			}
		}
		probe := NewEngine(For("hard"))
		probe.deadline = time.Now().Add(time.Hour)
		probe.pos.load(&b)
		if anyFive(&b) || probe.pos.hasFive(game.Black) || probe.pos.hasFive(game.White) {
			continue
		}
		if _, ok := probe.solveVCF(game.White, 12); ok {
			continue // we could just win; not a defence question
		}
		if _, ok := probe.solveVCF(game.Black, 12); !ok {
			continue
		}
		// Black threatens a VCF; White to move must break it if any move can.
		// A shallow search with a long budget: the defence filter (which may
		// use a third of the budget) is under test, not the search, and
		// -race slows everything tenfold.
		cfg := For("hard")
		cfg.MaxDepth, cfg.Think = 3, 6*time.Second
		e := NewEngine(cfg)
		x, y, ok := e.Move(&b, game.White, time.Now().Add(6*time.Second))
		if !ok {
			t.Fatal("no move")
		}
		b[y][x] = game.White
		probe.pos.load(&b)
		clear(probe.vcfFail)
		probe.stats = Stats{}
		_, still := probe.solveVCF(game.Black, 12)
		b[y][x] = game.Empty
		if still && breakable(&b) {
			t.Fatalf("trial %d: (%d,%d) leaves black a VCF although a defence exists", trial, x, y)
		}
		checked++
	}
	if checked < 5 {
		t.Fatalf("only %d defence positions sampled", checked)
	}
}

// breakable reports whether some white move leaves black without a VCF.
func breakable(b *game.Board) bool {
	probe := NewEngine(For("hard"))
	probe.deadline = time.Now().Add(time.Hour)
	for y := range size {
		for x := range size {
			if b[y][x] != game.Empty {
				continue
			}
			b[y][x] = game.White
			probe.pos.load(b)
			clear(probe.vcfFail)
			probe.stats = Stats{}
			_, still := probe.solveVCF(game.Black, 12)
			b[y][x] = game.Empty
			if !still {
				return true
			}
		}
	}
	return false
}

// A reported VCT must hold against a real defender: the attacker follows
// the solver move by move, the hard engine defends, and the attacker must
// reach five. (The defender may miss the best defence, so this is an
// empirical check; the solver's defender set is an over-approximation by
// construction.)
func TestVCTHoldsAgainstSearch(t *testing.T) {
	if testing.Short() || raceOn {
		t.Skip("plays full games; single goroutine, nothing for -race to find")
	}
	r := rand.New(rand.NewPCG(5, 9))
	checked := 0
	for trial := 0; trial < 600 && checked < 12; trial++ {
		var b game.Board
		for i := 0; i < 14+r.IntN(16); i++ {
			x, y := 4+r.IntN(7), 4+r.IntN(7)
			if b[y][x] == game.Empty {
				b[y][x] = game.Color(1 + i%2)
			}
		}
		att := NewEngine(For("hard"))
		att.cfg.VCTBudget = 1 << 22 // soundness is under test here, not the node budget
		att.deadline = time.Now().Add(time.Hour)
		att.pos.load(&b)
		if anyFive(&b) || att.pos.hasFive(game.Black) || att.pos.hasFive(game.White) {
			continue
		}
		if _, ok := att.solveVCF(game.White, 12); ok {
			continue // White (to defend) could simply win first
		}
		if _, ok := att.solveVCT(game.White, 4); ok {
			continue
		}
		clear(att.vcfFail)
		if _, ok := att.solveVCF(game.Black, 12); ok {
			continue // a plain VCF is covered elsewhere
		}
		clear(att.vcfFail)
		att.stats = Stats{}
		if _, ok := att.solveVCT(game.Black, 4); !ok {
			continue
		}
		checked++
		def := engine("hard", 150*time.Millisecond)
		won := false
		for step := 0; step < 30 && !won; step++ {
			att.pos.load(&b)
			clear(att.vcfFail)
			att.stats = Stats{}
			m, ok := att.solveVCF(game.Black, 12)
			if !ok {
				clear(att.vcfFail)
				m, ok = att.solveVCT(game.Black, 4)
			}
			if !ok {
				t.Fatalf("trial %d step %d: the VCT evaporated against the defence", trial, step)
			}
			b[m.y][m.x] = game.Black
			if b.WinFrom(m.x, m.y) {
				won = true
				break
			}
			x, y, _ := def.Move(&b, game.White, time.Now().Add(time.Second))
			b[y][x] = game.White
			if b.WinFrom(x, y) {
				t.Fatalf("trial %d: defender won against a reported VCT", trial)
			}
		}
		if !won {
			t.Fatalf("trial %d: VCT did not finish", trial)
		}
	}
	t.Logf("VCT positions played out: %d", checked)
	if checked < 5 {
		t.Fatalf("only %d VCT positions sampled", checked)
	}
}
