package ai

import (
	"cmp"
	"slices"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

const (
	inf       = 4 * winScore
	mateBound = winScore - 1000 // |score| above this is a forced win/loss
)

type scored struct {
	m move
	s int
}

// candidates lists the empty cells within two of a stone with the best
// limit of them first, ordered by the static score (own shape plus a little
// more for the shape it denies the opponent). The slice is the per-ply
// buffer: valid until the next call at the same ply.
func (e *Engine) candidates(me game.Color, ply, limit int) []scored {
	p := &e.pos
	out := e.cand[ply][:0]
	for y := range size {
		for x := range size {
			if p.cell[y][x] != game.Empty || p.near[y][x] == 0 {
				continue
			}
			b, w := p.pointScores(x, y)
			mine, theirs := b, w
			if me == game.White {
				mine, theirs = w, b
			}
			out = append(out, scored{move{x, y}, mine + theirs*11/10 + centerBias(x, y)})
		}
	}
	topK(out, limit)
	e.cand[ply] = out
	return out
}

// topK moves the k best entries, in order, to the front of a (partial
// selection sort: a node searches about a dozen of ~80 candidates, so a
// full sort is wasted work). Ties keep board order, so search is
// deterministic.
func topK(a []scored, k int) {
	k = min(k, len(a))
	for i := 0; i < k; i++ {
		best := i
		for j := i + 1; j < len(a); j++ {
			if a[j].s > a[best].s {
				best = j
			}
		}
		if best != i {
			hit := a[best]
			copy(a[i+1:best+1], a[i:best])
			a[i] = hit
		}
	}
}

func (e *Engine) rootCandidates(me game.Color) []scored {
	n := e.cfg.Breadth * 2
	c := e.candidates(me, 0, n)
	if len(c) > n {
		c = c[:n]
	}
	return slices.Clone(c)
}

// iterate deepens one ply at a time and keeps the best move of the last
// iteration that finished. It stops early on a proven result, or when the
// time already spent says the next (several times larger) iteration would
// not finish anyway.
func (e *Engine) iterate(me game.Color, cands []scored) move {
	best := cands[0].m
	start := time.Now()
	budget := e.deadline.Sub(start)
	for d := 1; d <= e.cfg.MaxDepth; d++ {
		m, score, done := e.searchRoot(me, cands, d)
		if !done {
			break
		}
		best = m
		e.stats.Depth = d
		if score > mateBound || score < -mateBound {
			break
		}
		if time.Since(start) > budget*45/100 {
			break
		}
	}
	return best
}

// searchRoot runs one iteration over the root moves (principal variation
// search: the first move gets a full window, the rest a null window that is
// re-searched only if it beats the best so far). Root moves are re-sorted
// by score so the next iteration starts from the current best.
func (e *Engine) searchRoot(me game.Color, cands []scored, depth int) (move, int, bool) {
	p := &e.pos
	opp := me.Other()
	alpha := -inf
	best, bestScore := cands[0].m, -inf
	pvs := e.cfg.NoisePct == 0 // noisy scores need exact values, not bounds
	for i := range cands {
		c := cands[i].m
		p.place(c.x, c.y, me)
		var s int
		if i == 0 || !pvs {
			s = -e.negamax(opp, depth-1, -inf, -alpha, 1)
		} else {
			s = -e.negamax(opp, depth-1, -alpha-1, -alpha, 1)
			if s > alpha && !e.stop {
				s = -e.negamax(opp, depth-1, -inf, -alpha, 1)
			}
		}
		p.remove(c.x, c.y)
		if e.stop {
			return best, bestScore, false
		}
		if e.cfg.NoisePct > 0 {
			s += e.rnd.IntN(max(1, abs(s)*e.cfg.NoisePct/100) + 1)
		}
		cands[i].s = s
		if s > bestScore {
			best, bestScore = c, s
		}
		alpha = max(alpha, s)
	}
	slices.SortStableFunc(cands, func(a, b scored) int { return cmp.Compare(b.s, a.s) })
	return best, bestScore, true
}

// negamax is alpha-beta from the side to move's view. Exact tactics come
// first and bypass the table: a five to complete wins, two opposing fives
// lose, a single opposing five is the only legal reply (searched without
// spending depth, so forcing lines are read to the end).
func (e *Engine) negamax(me game.Color, depth, alpha, beta, ply int) int {
	p := &e.pos
	e.stats.Nodes++
	if e.stats.Nodes&511 == 0 {
		e.expired()
	}
	if e.stop {
		return 0
	}
	opp := me.Other()
	if p.hasFive(me) {
		return winScore - ply
	}
	threats := p.fiveCells(opp, e.threats[ply][:0])
	if len(threats) >= 2 {
		return -(winScore - ply - 1)
	}
	if p.stones == size*size {
		return 0
	}
	if ply >= maxPly-1 || (depth <= 0 && len(threats) == 0) {
		return p.eval(me)
	}

	key := p.key ^ sideKey[me]
	alphaOrig := alpha
	var ttMove move
	hasTT := false
	if ent, ok := e.tt.probe(key); ok {
		e.stats.TTHits++
		ttMove, hasTT = ent.bestMove()
		if int(ent.depth) >= depth {
			s := fromTT(int(ent.score), ply)
			switch ent.flag {
			case ttExact:
				return s
			case ttLower:
				alpha = max(alpha, s)
			case ttUpper:
				beta = min(beta, s)
			}
			if alpha >= beta {
				return s
			}
		}
	}

	var moves []scored
	childDepth := depth - 1
	if len(threats) == 1 {
		moves = append(e.cand[ply][:0], scored{m: threats[0]})
		childDepth = depth // forced reply: extend
	} else {
		moves = e.candidates(me, ply, e.cfg.Breadth)
		e.promote(moves, ttMove, hasTT, ply)
		if len(moves) > e.cfg.Breadth {
			moves = moves[:e.cfg.Breadth]
		}
	}

	best, bestMove := -inf, moves[0].m
	for i := range moves {
		c := moves[i].m
		p.place(c.x, c.y, me)
		var s int
		if i == 0 {
			s = -e.negamax(opp, childDepth, -beta, -alpha, ply+1)
		} else {
			s = -e.negamax(opp, childDepth, -alpha-1, -alpha, ply+1)
			if s > alpha && s < beta && !e.stop {
				s = -e.negamax(opp, childDepth, -beta, -alpha, ply+1)
			}
		}
		p.remove(c.x, c.y)
		if e.stop {
			return 0
		}
		if s > best {
			best, bestMove = s, c
		}
		if s > alpha {
			alpha = s
		}
		if alpha >= beta {
			if k := &e.killers[ply]; k[0] != c {
				k[1], k[0] = k[0], c
			}
			break
		}
	}

	flag := ttExact
	switch {
	case best <= alphaOrig:
		flag = ttUpper
	case best >= beta:
		flag = ttLower
	}
	e.tt.store(key, depth, toTT(best, ply), flag, bestMove, true)
	return best
}

// promote moves the table's best move to the front and this ply's killer
// moves (recent cutoffs at the same depth) right behind it.
func (e *Engine) promote(moves []scored, tt move, hasTT bool, ply int) {
	front := 0
	lift := func(m move) {
		for i := front; i < len(moves); i++ {
			if moves[i].m == m {
				hit := moves[i]
				copy(moves[front+1:i+1], moves[front:i])
				moves[front] = hit
				front++
				return
			}
		}
	}
	if hasTT {
		lift(tt)
	}
	lift(e.killers[ply][0])
	lift(e.killers[ply][1])
}
