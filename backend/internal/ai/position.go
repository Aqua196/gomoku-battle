package ai

import (
	"math/bits"
	"math/rand/v2"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// Pattern weights by stone count inside a five-cell window that holds no
// opposing stone. Bonuses mark shapes that windows alone under-rate.
const (
	winScore       = 1 << 24
	openThreeBonus = 3200  // .XXX. or .X.XX. — one move from an open four
	openFourBonus  = 64000 // two five-completions on one line: unstoppable
)

var patScore = [6]int{0, 8, 90, 1100, 16000, winScore}

// zobrist[y][x][color] and sideKey[color] are the hashing keys. A fixed
// seed keeps keys (and therefore search order) reproducible across runs.
var (
	zobrist [size][size][3]uint64
	sideKey [3]uint64
)

func init() {
	r := rand.New(rand.NewPCG(0x60_6b, 0x5eed))
	for y := range size {
		for x := range size {
			zobrist[y][x][game.Black] = r.Uint64()
			zobrist[y][x][game.White] = r.Uint64()
		}
	}
	sideKey[game.Black], sideKey[game.White] = r.Uint64(), r.Uint64()
}

// lineInfo caches what one line is worth to each color (indexed by
// game.Color; slot 0 unused).
type lineInfo struct {
	score [3]int
	five  [3]uint16 // bit i: playing index i completes five
	four  [3]uint16 // bit i: playing index i makes a four (a five-threat)
	three [3]uint16 // bit i: playing index i puts a third stone in a clean window
}

// position is the engine's private board. Every placement updates, in
// O(4 lines) instead of O(whole board): the Zobrist key, the pattern score
// and threat masks of the four lines through the cell, and the
// neighbourhood counts that define candidate moves.
type position struct {
	cell   [size][size]game.Color
	near   [size][size]int // stones within Chebyshev distance 2
	key    uint64
	stones int
	lines  [maxLines]lineInfo
	total  [3]int // sum of line scores per color
}

func (p *position) load(b *game.Board) {
	*p = position{}
	for y := range size {
		for x := range size {
			if c := b[y][x]; c != game.Empty {
				p.place(x, y, c)
			}
		}
	}
}

func (p *position) place(x, y int, c game.Color) {
	p.cell[y][x] = c
	p.key ^= zobrist[y][x][c]
	p.stones++
	p.touch(x, y, 1)
	p.rescoreAround(x, y)
}

func (p *position) remove(x, y int) {
	c := p.cell[y][x]
	p.cell[y][x] = game.Empty
	p.key ^= zobrist[y][x][c]
	p.stones--
	p.touch(x, y, -1)
	p.rescoreAround(x, y)
}

func (p *position) touch(x, y, delta int) {
	for ny := max(0, y-2); ny <= min(size-1, y+2); ny++ {
		for nx := max(0, x-2); nx <= min(size-1, x+2); nx++ {
			p.near[ny][nx] += delta
		}
	}
}

func (p *position) rescoreAround(x, y int) {
	for d := range dirs {
		if ref := cellLine[y][x][d]; ref.line != noLine {
			p.rescore(ref.line)
		}
	}
}

func (p *position) rescore(id int) {
	cells := lineCells[id]
	var v [size]game.Color
	for i, c := range cells {
		v[i] = p.cell[c[1]][c[0]]
	}
	li := &p.lines[id]
	p.total[game.Black] -= li.score[game.Black]
	p.total[game.White] -= li.score[game.White]
	li.score, li.five, li.four, li.three = scanBoth(v[:len(cells)])
	p.total[game.Black] += li.score[game.Black]
	p.total[game.White] += li.score[game.White]
}

// scanLine scores one line for color c; see scanBoth.
func scanLine(v []game.Color, c game.Color) (score int, five, four uint16) {
	sc, fv, fr, _ := scanBoth(v)
	return sc[c], fv[c], fr[c]
}

// scanBoth scores one line for both colors in a single pass of sliding
// five-cell windows. A window holding only one color's stones adds
// patScore[stones] for that color; with four stones its empty cell is a
// five-completion, with three both empties are four-makers, with two the
// three empties are three-makers. Open threes
// (.XXX. and the split .X.XX.) and lines with two completions earn bonuses.
func scanBoth(v []game.Color) (score [3]int, five, four, three [3]uint16) {
	n := len(v)
	var cnt [3]int // stones per color inside the current window
	for k := range 5 {
		cnt[v[k]]++
	}
	for i := 0; ; i++ {
		var c game.Color
		switch {
		case cnt[game.White] == 0 && cnt[game.Black] > 0:
			c = game.Black
		case cnt[game.Black] == 0 && cnt[game.White] > 0:
			c = game.White
		}
		if c != game.Empty {
			k := cnt[c]
			score[c] += patScore[k]
			if k >= 2 {
				var m uint16
				for j := i; j < i+5; j++ {
					if v[j] == game.Empty {
						m |= 1 << j
					}
				}
				switch k {
				case 4:
					five[c] |= m
				case 3:
					four[c] |= m
					if v[i] == game.Empty && v[i+4] == game.Empty {
						score[c] += openThreeBonus // .XXX.
					}
				case 2:
					three[c] |= m
				}
			}
		}
		if i+5 >= n {
			break
		}
		cnt[v[i]]--
		cnt[v[i+5]]++
	}
	// Split open threes: a six-cell frame, empty at both ends, holding
	// X.XX or XX.X inside.
	for i := 0; i+6 <= n; i++ {
		c := v[i+1]
		if c == game.Empty || v[i] != game.Empty || v[i+5] != game.Empty || v[i+4] != c {
			continue
		}
		if (v[i+2] == c && v[i+3] == game.Empty) || (v[i+2] == game.Empty && v[i+3] == c) {
			score[c] += openThreeBonus
		}
	}
	for _, c := range [2]game.Color{game.Black, game.White} {
		if bits.OnesCount16(five[c]) >= 2 {
			score[c] += openFourBonus
		}
	}
	return score, five, four, three
}

// eval scores the position for the side to move. The mover's own shapes
// count a little extra: having the move is worth a tempo.
func (p *position) eval(me game.Color) int {
	return p.total[me]*11/10 - p.total[me.Other()]
}

// hasFive reports whether c can complete five with its next stone.
func (p *position) hasFive(c game.Color) bool {
	for i := range p.lines {
		if p.lines[i].five[c] != 0 {
			return true
		}
	}
	return false
}

// fiveCells appends every distinct cell where c completes five.
func (p *position) fiveCells(c game.Color, out []move) []move {
	for id := range p.lines {
		out = appendMask(out, id, p.lines[id].five[c])
	}
	return out
}

// fourMoves appends every distinct cell where c makes a four.
func (p *position) fourMoves(c game.Color, out []move) []move {
	for id := range p.lines {
		out = appendMask(out, id, p.lines[id].four[c])
	}
	return out
}

func appendMask(out []move, id int, m uint16) []move {
	for m != 0 {
		i := bits.TrailingZeros16(m)
		m &= m - 1
		c := lineCells[id][i]
		mv := move{c[0], c[1]}
		dup := false
		for _, o := range out {
			if o == mv {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, mv)
		}
	}
	return out
}

// threeMakers appends every distinct cell where c adds a third stone to a
// window free of the opponent — the superset of moves that make a three.
func (p *position) threeMakers(c game.Color, out []move) []move {
	for id := range p.lines {
		out = appendMask(out, id, p.lines[id].three[c])
	}
	return out
}

// fiveAt reports whether the stone at (x,y) is part of five or more.
func (p *position) fiveAt(x, y int) bool {
	c := p.cell[y][x]
	for _, d := range dirs {
		if 1+p.run(x, y, d[0], d[1], c)+p.run(x, y, -d[0], -d[1], c) >= 5 {
			return true
		}
	}
	return false
}

func (p *position) run(x, y, dx, dy int, c game.Color) int {
	n := 0
	for nx, ny := x+dx, y+dy; game.InBounds(nx, ny) && p.cell[ny][nx] == c; nx, ny = nx+dx, ny+dy {
		n++
	}
	return n
}

// side looks from (x,y) towards (dx,dy): the color of the adjacent stones
// (Empty when the neighbour is empty or off the board), how many of them
// there are in a row, and whether the cell after them (or the neighbour
// itself, when empty) is an empty cell on the board.
func (p *position) side(x, y, dx, dy int) (game.Color, int, bool) {
	nx, ny := x+dx, y+dy
	if !game.InBounds(nx, ny) {
		return game.Empty, 0, false
	}
	c := p.cell[ny][nx]
	if c == game.Empty {
		return game.Empty, 0, true
	}
	n := 0
	for game.InBounds(nx, ny) && p.cell[ny][nx] == c {
		n++
		nx += dx
		ny += dy
	}
	return c, n, game.InBounds(nx, ny) && p.cell[ny][nx] == game.Empty
}

// pointScores rates placing a stone at empty (x,y) for black and white at
// once (move ordering): one walk per direction serves both colors, since a
// run belongs to whichever color sits next to the cell.
func (p *position) pointScores(x, y int) (black, white int) {
	var acc [3]shapeAcc
	for _, d := range dirs {
		fc, fn, fOpen := p.side(x, y, d[0], d[1])
		bc, bn, bOpen := p.side(x, y, -d[0], -d[1])
		for _, c := range [2]game.Color{game.Black, game.White} {
			fwd, openF := runFor(c, fc, fn, fOpen)
			bwd, openB := runFor(c, bc, bn, bOpen)
			acc[c].add(1+fwd+bwd, openF, openB)
		}
	}
	return acc[game.Black].total(), acc[game.White].total()
}

// pointScore is pointScores for one color.
func (p *position) pointScore(x, y int, c game.Color) int {
	b, w := p.pointScores(x, y)
	if c == game.Black {
		return b
	}
	return w
}

// runFor translates a side observation into c's run length and openness.
func runFor(c, sideColor game.Color, n int, open bool) (int, bool) {
	switch sideColor {
	case c:
		return n, open
	case game.Empty:
		return 0, open // empty neighbour: open iff it is on the board
	}
	return 0, false // opponent stone next to the cell
}

// shapeAcc sums the per-axis shapes one placement creates: open and
// half-open runs, plus fork bonuses (four-three, double three).
type shapeAcc struct {
	sum, openThrees, fours int
	five                   bool
}

func (a *shapeAcc) add(n int, openF, openB bool) {
	if n >= 5 {
		a.five = true
		return
	}
	ends := 0
	if openF {
		ends++
	}
	if openB {
		ends++
	}
	if ends == 0 {
		return
	}
	switch n {
	case 4:
		a.sum += [3]int{0, 18000, 120000}[ends]
		a.fours++
	case 3:
		a.sum += [3]int{0, 900, 9000}[ends]
		if ends == 2 {
			a.openThrees++
		}
	case 2:
		a.sum += [3]int{0, 60, 450}[ends]
	case 1:
		a.sum += [3]int{0, 2, 10}[ends]
	}
}

func (a *shapeAcc) total() int {
	switch {
	case a.five:
		return winScore
	case a.fours >= 2 || (a.fours >= 1 && a.openThrees >= 1):
		return a.sum + 80000
	case a.openThrees >= 2:
		return a.sum + 40000
	}
	return a.sum
}

func centerBias(x, y int) int {
	mid := size / 2
	return 14 - abs(x-mid) - abs(y-mid)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
