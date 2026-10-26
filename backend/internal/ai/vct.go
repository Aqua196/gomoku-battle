package ai

import (
	"slices"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// VCT — victory by continuous threats: fours and open threes. A four
// leaves one reply; an open three leaves a handful (spoil the three, or
// counter with a four), and the attack only counts if it beats every one
// of them. The defender's options are over-approximated (every cell in one
// of the attacker's three-stone windows, plus every defender four), which
// can only make the solver miss wins, never invent them.

var vctSalt = [3]uint64{0, 0x3c6e_f372_fe94_f82b, 0xa54f_f53a_5f1d_36f1}

// vctBreadth caps the threats tried per attacker node.
const vctBreadth = 10

// solveVCT looks for a VCT for att (to move) with at most depth threat
// moves, within the configured node budget.
func (e *Engine) solveVCT(att game.Color, depth int) (move, bool) {
	budget := e.stats.VCFNodes + e.cfg.VCTBudget
	return e.vct(att, depth, budget, 0)
}

func (e *Engine) vct(att game.Color, depth, budget, ply int) (move, bool) {
	p := &e.pos
	def := att.Other()
	e.stats.VCFNodes++
	if e.stats.VCFNodes > budget || ply >= maxPly-6 || e.stop {
		return move{}, false
	}
	// A VCF is a VCT; the cheaper solver also settles fives and blocks.
	if m, ok := e.vcf(att, min(e.cfg.VCFDepth, 8), budget, ply); ok {
		return m, true
	}
	if depth == 0 || e.stop || len(p.fiveCells(def, e.threats[ply][:0])) > 0 {
		// Against a five threat only fours help, and the VCF tried them.
		return move{}, false
	}
	memo := p.key ^ vctSalt[att]
	if d, seen := e.vcfFail[memo]; seen && d >= depth {
		return move{}, false
	}

	for _, m := range e.threatMoves(att) {
		p.place(m.x, m.y, att)
		won := false
		switch wins := p.fiveCells(att, e.threats[ply+1][:0]); len(wins) {
		case 0: // an open three, if anything
			if e.hasOpenFourMove(att, ply+1) {
				won = e.beatsEveryDefence(att, depth, budget, ply)
			}
		case 1: // a four: the reply is forced
			b := wins[0]
			e.stats.VCFNodes++
			p.place(b.x, b.y, def)
			if !p.fiveAt(b.x, b.y) {
				_, won = e.vct(att, depth-1, budget, ply+2)
			}
			p.remove(b.x, b.y)
		default:
			won = true // open or double four (the VCF should have caught it)
		}
		p.remove(m.x, m.y)
		if won {
			return m, true
		}
		if e.stop {
			return move{}, false
		}
	}
	if !e.stop && e.stats.VCFNodes <= budget {
		e.vcfFail[memo] = depth
	}
	return move{}, false
}

// beatsEveryDefence: the attacker just made an open three. Try each
// defender reply; the attack holds only if a VCT follows all of them.
func (e *Engine) beatsEveryDefence(att game.Color, depth, budget, ply int) bool {
	p := &e.pos
	def := att.Other()
	defs := p.fourMoves(att, e.fours[ply+1][:0])
	defs = p.fourMoves(def, defs)
	for _, d := range defs {
		e.stats.VCFNodes++
		p.place(d.x, d.y, def)
		ok := !p.fiveAt(d.x, d.y)
		if ok {
			_, ok = e.vct(att, depth-1, budget, ply+2)
		}
		p.remove(d.x, d.y)
		if !ok {
			return false
		}
	}
	return len(defs) > 0
}

// threatMoves lists att's candidate threats — cells that make a four or
// put a third stone in a clean window — best-first and capped.
func (e *Engine) threatMoves(att game.Color) []move {
	p := &e.pos
	cells := p.fourMoves(att, nil)
	cells = p.threeMakers(att, cells)
	type rated struct {
		m move
		s int
	}
	rs := make([]rated, 0, len(cells))
	for _, m := range cells {
		rs = append(rs, rated{m, p.pointScore(m.x, m.y, att)})
	}
	slices.SortStableFunc(rs, func(a, b rated) int { return b.s - a.s })
	out := make([]move, 0, vctBreadth)
	for _, r := range rs[:min(len(rs), vctBreadth)] {
		out = append(out, r.m)
	}
	return out
}

// hasOpenFourMove reports whether att has a move that makes two
// five-completions at once.
func (e *Engine) hasOpenFourMove(att game.Color, ply int) bool {
	p := &e.pos
	for _, m := range p.fourMoves(att, e.threats[ply][:0]) {
		p.place(m.x, m.y, att)
		n := len(p.fiveCells(att, e.fours[ply][:0]))
		p.remove(m.x, m.y)
		if n >= 2 {
			return true
		}
	}
	return false
}
