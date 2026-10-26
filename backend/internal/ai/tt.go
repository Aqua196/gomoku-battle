package ai

// Transposition table: positions reached through different move orders
// share one entry keyed by Zobrist hash, so the search reuses bounds and
// best moves instead of re-deriving them. It lives as long as the Engine,
// i.e. one AI game, and carries knowledge from one move to the next.

const (
	ttExact = iota + 1
	ttLower // score is a lower bound (beta cutoff)
	ttUpper // score is an upper bound (no move raised alpha)
)

type ttEntry struct {
	key   uint64
	score int32
	depth int16
	flag  uint8
	move  int16 // y*size+x, -1 for none
}

type ttable struct {
	entries []ttEntry
	mask    uint64
}

// newTT builds a table of 1<<bitsN entries (16 bytes each); 0 disables it.
func newTT(bitsN uint) *ttable {
	if bitsN == 0 {
		return nil
	}
	n := uint64(1) << bitsN
	return &ttable{entries: make([]ttEntry, n), mask: n - 1}
}

func (t *ttable) probe(key uint64) (ttEntry, bool) {
	if t == nil {
		return ttEntry{}, false
	}
	e := t.entries[key&t.mask]
	return e, e.key == key && e.flag != 0
}

// store keeps the deeper result when two positions share a slot.
func (t *ttable) store(key uint64, depth, score, flag int, mv move, hasMove bool) {
	if t == nil {
		return
	}
	slot := &t.entries[key&t.mask]
	if slot.key == key && int(slot.depth) > depth {
		return
	}
	packed := int16(-1)
	if hasMove {
		packed = int16(mv.y*size + mv.x) //nolint:gosec // < 225
	}
	*slot = ttEntry{
		key:   key,
		score: int32(score), //nolint:gosec // |score| <= 2*winScore fits int32
		depth: int16(depth), //nolint:gosec // depth <= maxPly
		flag:  uint8(flag),  //nolint:gosec // one of the tt* constants
		move:  packed,
	}
}

func (e ttEntry) bestMove() (move, bool) {
	if e.move < 0 {
		return move{}, false
	}
	return move{int(e.move) % size, int(e.move) / size}, true
}

// Mate scores are stored relative to the node, not the root, so a win
// found at one depth reads correctly when the position recurs at another.
func toTT(score, ply int) int {
	switch {
	case score > winScore-1000:
		return score + ply
	case score < -(winScore - 1000):
		return score - ply
	}
	return score
}

func fromTT(score, ply int) int {
	switch {
	case score > winScore-1000:
		return score - ply
	case score < -(winScore - 1000):
		return score + ply
	}
	return score
}
