package ai

import (
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// VCF — victory by continuous fours. The attacker only plays moves that
// threaten five, so every defender reply is forced (block the one
// completion cell); a four with two completion cells cannot be blocked at
// all. The tree is therefore narrow and deep: a 10-move combination costs
// thousands of nodes here, where full-width alpha-beta would need depth 20.

// vcfSalt separates "black has no VCF" from "white has no VCF" for the
// same stones in the failure memo.
var vcfSalt = [3]uint64{0, 0x9d1f_31aa_74c2_51e5, 0x47b3_e06c_28a9_dd13}

// solveVCF looks for a VCF for att (to move) within depth attacker moves
// and the configured node budget, returning its first move.
func (e *Engine) solveVCF(att game.Color, depth int) (move, bool) {
	budget := e.stats.VCFNodes + e.cfg.VCFBudget
	return e.vcf(att, depth, budget, 0)
}

func (e *Engine) vcf(att game.Color, depth, budget, ply int) (move, bool) {
	p := &e.pos
	def := att.Other()
	e.stats.VCFNodes++
	if e.stats.VCFNodes > budget || ply >= maxPly-2 || (e.stats.VCFNodes&255 == 0 && e.expired()) || e.stop {
		return move{}, false
	}
	if fives := p.fiveCells(att, e.threats[ply][:0]); len(fives) > 0 {
		return fives[0], true
	}
	if depth == 0 {
		return move{}, false
	}
	memo := p.key ^ vcfSalt[att]
	if d, seen := e.vcfFail[memo]; seen && d >= depth {
		return move{}, false
	}

	defFives := p.fiveCells(def, e.threats[ply][:0])
	if len(defFives) >= 2 {
		return move{}, false // the defender completes five whatever we do
	}
	moves := p.fourMoves(att, e.fours[ply][:0])
	if len(defFives) == 1 {
		// We must block their five, and the block must itself be a four.
		must := defFives[0]
		forced := moves[:0]
		for _, m := range moves {
			if m == must {
				forced = append(forced, m)
				break
			}
		}
		moves = forced
	}

	for _, m := range moves {
		p.place(m.x, m.y, att)
		wins := p.fiveCells(att, e.threats[ply+1][:0])
		found := false
		switch {
		case len(wins) >= 2:
			found = true // open four / double four: only one can be blocked
		case len(wins) == 1:
			b := wins[0]
			p.place(b.x, b.y, def)
			if !p.fiveAt(b.x, b.y) { // a block that makes five wins for them
				_, found = e.vcf(att, depth-1, budget, ply+2)
			}
			p.remove(b.x, b.y)
		}
		p.remove(m.x, m.y)
		if found {
			return m, true
		}
		if e.stop {
			return move{}, false
		}
	}
	if !e.stop && e.stats.VCFNodes <= budget {
		e.vcfFail[memo] = depth // a budget cut proves nothing, so only record clean failures
	}
	return move{}, false
}

// defendVCF drops root candidates after which the opponent (then to move)
// still has a forced win. Breaking a VCF comes first; among the moves that
// do, those that also break a VCT (when that solver is enabled) are
// preferred. A filter that no move survives is skipped (that threat cannot
// be stopped, so the search should at least pick the most stubborn move).
func (e *Engine) defendVCF(me game.Color, cands []scored) []scored {
	opp := me.Other()
	cands = e.dropLosing(me, cands, func() bool {
		_, ok := e.solveVCF(opp, e.cfg.VCFDepth)
		return ok
	})
	if e.cfg.VCTDepth > 0 {
		cands = e.dropLosing(me, cands, func() bool {
			_, ok := e.solveVCT(opp, e.cfg.VCTDepth)
			return ok
		})
	}
	return cands
}

// dropLosing keeps the candidates after which threat() (the opponent's
// solver, opponent to move) fails. It only runs when the threat exists now.
// Checks stop at a third of the remaining budget; unchecked candidates are
// kept for the search to judge.
func (e *Engine) dropLosing(me game.Color, cands []scored, threat func() bool) []scored {
	if !threat() {
		return cands
	}
	p := &e.pos
	stopAt := time.Now().Add(time.Until(e.deadline) / 3)
	kept := make([]scored, 0, len(cands))
	for i, c := range cands {
		if time.Now().After(stopAt) || e.stop {
			kept = append(kept, cands[i:]...)
			break
		}
		p.place(c.m.x, c.m.y, me)
		still := threat()
		p.remove(c.m.x, c.m.y)
		if !still {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return cands
	}
	return kept
}
