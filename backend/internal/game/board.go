// Package game implements the server-authoritative gomoku rules (PROTOCOL §4)
// as pure board logic plus a per-game actor goroutine that serializes all
// state mutations through a mailbox (DESIGN §3).
package game

import "github.com/Aqua196/gomoku-battle/backend/internal/protocol"

// Color is both a seat identity and a board cell value: empty=0, black=1,
// white=2 — identical to the wire values in PROTOCOL §0.
type Color int8

const (
	Empty Color = protocol.CellEmpty
	Black Color = protocol.CellBlack
	White Color = protocol.CellWhite
)

// Other returns the opposing color; Empty stays Empty.
func (c Color) Other() Color {
	switch c {
	case Black:
		return White
	case White:
		return Black
	}
	return Empty
}

// Name is the wire form ("black"/"white").
func (c Color) Name() string {
	switch c {
	case Black:
		return protocol.ColorBlack
	case White:
		return protocol.ColorWhite
	}
	return ""
}

const BoardSize = protocol.BoardSize

// Board is board[y][x]: y = row, x = column.
type Board [BoardSize][BoardSize]Color

// InBounds reports whether (x,y) is on the board.
func InBounds(x, y int) bool {
	return x >= 0 && x < BoardSize && y >= 0 && y < BoardSize
}

// CanPlace reports whether the cell is in bounds and empty.
func (b *Board) CanPlace(x, y int) bool {
	return InBounds(x, y) && b[y][x] == Empty
}

// Place drops a stone; the caller must have checked CanPlace.
func (b *Board) Place(x, y int, c Color) {
	b[y][x] = c
}

// WinFrom counts consecutive stones of the just-placed color through (x,y)
// on all four axes. Free-style rule: >=5 in a row wins, overlines included.
func (b *Board) WinFrom(x, y int) bool {
	c := b[y][x]
	if c == Empty {
		return false
	}
	dirs := [][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		if 1+b.run(x, y, d[0], d[1], c)+b.run(x, y, -d[0], -d[1], c) >= 5 {
			return true
		}
	}
	return false
}

// run counts stones of color c walking from (x,y) in direction (dx,dy),
// excluding the starting cell.
func (b *Board) run(x, y, dx, dy int, c Color) int {
	n := 0
	for nx, ny := x+dx, y+dy; InBounds(nx, ny) && b[ny][nx] == c; nx, ny = nx+dx, ny+dy {
		n++
	}
	return n
}

// Full reports whether every cell is occupied (225 stones => draw).
func (b *Board) Full() bool {
	for y := 0; y < BoardSize; y++ {
		for x := 0; x < BoardSize; x++ {
			if b[y][x] == Empty {
				return false
			}
		}
	}
	return true
}
