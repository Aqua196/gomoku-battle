package ai

import "github.com/Aqua196/gomoku-battle/backend/internal/game"

const size = game.BoardSize

// dirs are the four line axes: row, column, main diagonal, anti-diagonal.
var dirs = [4][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}}

// maxLines counts the board lines long enough to hold a five: 15 rows, 15
// columns and 21 diagonals each way (the 4 shortest per corner are skipped).
const maxLines = 2*size + 2*(2*size-9)

const noLine = -1

// lineRef locates a cell on one axis: which line, and where along it.
type lineRef struct {
	line int // noLine when that axis through the cell is shorter than five
	idx  int
}

var (
	// lineCells[id] lists the cells of line id in order, as {x, y}.
	lineCells [][][2]int
	// cellLine[y][x][d] is the cell's position on axis d.
	cellLine [size][size][4]lineRef
)

func init() {
	for y := range size {
		for x := range size {
			for d := range dirs {
				cellLine[y][x][d] = lineRef{line: noLine}
			}
		}
	}
	for d, dv := range dirs {
		for y := range size {
			for x := range size {
				if game.InBounds(x-dv[0], y-dv[1]) {
					continue // only start walking from the head of each line
				}
				var cells [][2]int
				for cx, cy := x, y; game.InBounds(cx, cy); cx, cy = cx+dv[0], cy+dv[1] {
					cells = append(cells, [2]int{cx, cy})
				}
				if len(cells) < 5 {
					continue
				}
				id := len(lineCells)
				lineCells = append(lineCells, cells)
				for i, c := range cells {
					cellLine[c[1]][c[0]][d] = lineRef{line: id, idx: i}
				}
			}
		}
	}
	if len(lineCells) != maxLines {
		panic("ai: line table size mismatch")
	}
}
