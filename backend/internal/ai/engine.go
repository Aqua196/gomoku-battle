// Package ai is the embedded gomoku opponent: an alpha-beta engine with an
// incrementally evaluated board, a transposition table and a VCF (victory
// by continuous fours) threat solver, plus the driver goroutine that feeds
// its moves back into the game actor's mailbox (driver.go). The AI never
// bypasses the actor — it posts Move events exactly like a human client
// (DESIGN §4, PROTOCOL §10).
//
// Files: lines.go (line tables), position.go (incremental board and
// evaluation), tt.go (transposition table), vcf.go (threat solver),
// search.go (iterative-deepening PVS). baseline/ freezes the v1 engine as
// the sparring partner for cmd/aiarena.
package ai

import (
	"math/rand/v2"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// Config is one difficulty preset. Think caps a single move's thinking
// time; running out returns the best move of the last finished iteration.
type Config struct {
	MaxDepth  int           // deepest iteration of the alpha-beta search
	Breadth   int           // candidate moves searched per node after ordering
	NoisePct  int           // root score jitter in percent (easy only)
	Think     time.Duration // per-move budget
	Reflex    bool          // shortcut: make / stop an open four before searching
	VCFDepth  int           // attacker moves the VCF solver may chain; 0 disables
	VCFBudget int           // node cap per VCF solve
	VCTDepth  int           // threats (fours, open threes) the VCT solver may chain; 0 disables
	VCTBudget int           // node cap per VCT solve
	Defend    bool          // drop root moves that leave the opponent a VCF / VCT
	TTBits    uint          // transposition table size, 1<<TTBits entries; 0 disables
}

// For returns the preset for a PROTOCOL §2 difficulty string. An unknown
// difficulty falls back to normal (the hub validates the string first, so
// only internal misuse can get here and a sane default beats an error).
//
//   - easy: two plies, a noisy root, no threat solving — beatable on purpose.
//   - normal: four plies plus short VCF attacks.
//   - hard: deepening until the budget runs out, VCF/VCT attack and defence.
func For(difficulty string) Config {
	switch difficulty {
	case "easy":
		return Config{MaxDepth: 2, Breadth: 6, NoisePct: 40, Think: 400 * time.Millisecond, Reflex: true}
	case "hard":
		return Config{MaxDepth: 32, Breadth: 12, Think: 1800 * time.Millisecond,
			VCFDepth: 12, VCFBudget: 40000, VCTDepth: 4, VCTBudget: 50000, Defend: true, TTBits: 16}
	default: // "normal" and any unknown value
		return Config{MaxDepth: 4, Breadth: 10, Think: 1200 * time.Millisecond, Reflex: true,
			VCFDepth: 6, VCFBudget: 4000, TTBits: 14}
	}
}

// Stats describes the last Move call, for logs and benchmarks.
type Stats struct {
	Depth    int           // deepest fully searched iteration
	Nodes    int           // alpha-beta nodes visited
	VCFNodes int           // threat-solver nodes visited
	TTHits   int           // table probes that matched the position
	Reason   string        // how the move was chosen: five|block|vcf|vct|reflex|search|open
	Elapsed  time.Duration // wall time spent
}

type move struct{ x, y int }

// maxPly bounds recursion, forced-reply extensions included.
const maxPly = 64

// Engine searches positions for one side of one game. It is not safe for
// concurrent use; the driver owns one per AI seat, which also lets the
// transposition table carry over between moves.
type Engine struct {
	cfg Config
	rnd *rand.Rand
	pos position
	tt  *ttable

	deadline time.Time
	cancel   <-chan struct{}
	stop     bool

	cand    [maxPly + 1][]scored
	threats [maxPly + 1][]move
	fours   [maxPly + 1][]move
	killers [maxPly + 1][2]move
	vcfFail map[uint64]int // positions where the attacker has no VCF, by depth tried
	stats   Stats
}

// NewEngine builds an engine from a difficulty preset.
func NewEngine(cfg Config) *Engine {
	e := &Engine{
		cfg:     cfg,
		rnd:     rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x9e3779b97f4a7c15)), //nolint:gosec // seed only
		tt:      newTT(cfg.TTBits),
		vcfFail: make(map[uint64]int),
	}
	for i := range e.cand {
		e.cand[i] = make([]scored, 0, size*size)
		e.threats[i] = make([]move, 0, 8)
		e.fours[i] = make([]move, 0, 32)
	}
	return e
}

// Stats reports how the last move was found.
func (e *Engine) Stats() Stats { return e.stats }

// Move picks the best placement for color me on b before deadline. ok is
// false only when no legal cell exists.
func (e *Engine) Move(b *game.Board, me game.Color, deadline time.Time) (x, y int, ok bool) {
	return e.MoveCancel(b, me, deadline, nil)
}

// MoveCancel is Move plus early abort on cancel: when the channel closes,
// the search unwinds and the best move found so far is returned.
//
// Order of business: complete our five; block theirs; run our VCF; make or
// stop an open four (Reflex presets); otherwise iterative-deepening PVS
// over the ordered candidates, minus those that hand the opponent a VCF.
func (e *Engine) MoveCancel(b *game.Board, me game.Color, deadline time.Time, cancel <-chan struct{}) (x, y int, ok bool) {
	if b == nil {
		return 0, 0, false
	}
	start := time.Now()
	e.deadline, e.cancel, e.stop = deadline, cancel, false
	if limit := start.Add(e.cfg.Think); e.cfg.Think > 0 && limit.Before(deadline) {
		e.deadline = limit
	}
	e.stats = Stats{}
	clear(e.vcfFail)
	e.pos.load(b)
	defer func() { e.stats.Elapsed = time.Since(start) }()

	m, reason, ok := e.choose(me)
	e.stats.Reason = reason
	return m.x, m.y, ok
}

func (e *Engine) choose(me game.Color) (move, string, bool) {
	p := &e.pos
	opp := me.Other()
	if p.stones == 0 {
		return move{size / 2, size / 2}, "open", true
	}
	if mine := p.fiveCells(me, nil); len(mine) > 0 {
		return mine[0], "five", true
	}
	if theirs := p.fiveCells(opp, nil); len(theirs) > 0 {
		return e.bestOf(theirs, me), "block", true
	}
	if e.cfg.VCFDepth > 0 {
		if m, ok := e.solveVCF(me, e.cfg.VCFDepth); ok {
			return m, "vcf", true
		}
	}
	if e.cfg.VCTDepth > 0 {
		if m, ok := e.solveVCT(me, e.cfg.VCTDepth); ok {
			return m, "vct", true
		}
	}
	if e.cfg.Reflex {
		if m, ok := e.openFourMove(me); ok {
			return m, "reflex", true
		}
		if m, ok := e.openFourMove(opp); ok {
			return m, "reflex", true
		}
	}

	cands := e.rootCandidates(me)
	if len(cands) == 0 {
		return e.anyEmpty()
	}
	if e.cfg.Defend {
		cands = e.defendVCF(me, cands)
	}
	return e.iterate(me, cands), "search", true
}

// bestOf picks, among forced cells, the one with the best ordering score.
func (e *Engine) bestOf(cells []move, me game.Color) move {
	best, bestScore := cells[0], -1
	for _, c := range cells {
		if s := e.pos.pointScore(c.x, c.y, me); s > bestScore {
			best, bestScore = c, s
		}
	}
	return best
}

// openFourMove finds a cell where c would make an open four (.XXXX.).
func (e *Engine) openFourMove(c game.Color) (move, bool) {
	p := &e.pos
	for _, m := range p.fourMoves(c, e.threats[0][:0]) {
		p.place(m.x, m.y, c)
		n := len(p.fiveCells(c, e.threats[1][:0]))
		p.remove(m.x, m.y)
		if n >= 2 {
			return m, true
		}
	}
	return move{}, false
}

func (e *Engine) anyEmpty() (move, string, bool) {
	for y := range size {
		for x := range size {
			if e.pos.cell[y][x] == game.Empty {
				return move{x, y}, "search", true
			}
		}
	}
	return move{}, "", false
}

// expired reports whether the search must unwind: budget spent, an abort
// already latched, or the cancel channel closed.
func (e *Engine) expired() bool {
	if e.stop {
		return true
	}
	if time.Now().After(e.deadline) {
		e.stop = true
		return true
	}
	if e.cancel != nil {
		select {
		case <-e.cancel:
			e.stop = true
			return true
		default:
		}
	}
	return false
}
