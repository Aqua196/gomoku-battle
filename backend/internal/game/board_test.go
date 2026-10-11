package game

import "testing"

// play places a stone or fails the test.
func play(t *testing.T, b *Board, x, y int, c Color) {
	t.Helper()
	if !b.CanPlace(x, y) {
		t.Fatalf("CanPlace(%d,%d) unexpectedly false", x, y)
	}
	b.Place(x, y, c)
}

// row plays a horizontal sequence for color c.
func row(t *testing.T, b *Board, y, x0, n int, c Color) {
	t.Helper()
	for i := 0; i < n; i++ {
		play(t, b, x0+i, y, c)
	}
}

// col plays a vertical sequence for color c.
func col(t *testing.T, b *Board, x, y0, n int, c Color) {
	t.Helper()
	for i := 0; i < n; i++ {
		play(t, b, x, y0+i, c)
	}
}

func TestRulesWinHorizontal(t *testing.T) {
	var b Board
	row(t, &b, 7, 3, 5, Black)
	if !b.WinFrom(7, 7) {
		t.Fatal("horizontal five not detected")
	}
}

func TestRulesWinVertical(t *testing.T) {
	var b Board
	col(t, &b, 4, 2, 5, White)
	if !b.WinFrom(4, 6) {
		t.Fatal("vertical five not detected")
	}
}

func TestRulesWinDiagonalMain(t *testing.T) {
	var b Board
	for i := 0; i < 5; i++ {
		play(t, &b, 5+i, 5+i, Black)
	}
	if !b.WinFrom(9, 9) {
		t.Fatal("main diagonal five not detected")
	}
}

func TestRulesWinDiagonalAnti(t *testing.T) {
	var b Board
	for i := 0; i < 5; i++ {
		play(t, &b, 10-i, 3+i, White)
	}
	if !b.WinFrom(6, 7) {
		t.Fatal("anti diagonal five not detected")
	}
}

func TestRulesWinThroughAnchor(t *testing.T) {
	// Anchor is in the middle of the run, not an endpoint: the counter must
	// walk both directions from the new stone.
	var b Board
	play(t, &b, 3, 7, Black)
	play(t, &b, 4, 7, Black)
	play(t, &b, 6, 7, Black)
	play(t, &b, 7, 7, Black)
	play(t, &b, 5, 7, Black) // fills the gap last
	if !b.WinFrom(5, 7) {
		t.Fatal("five completed at the anchor not detected")
	}
}

func TestRulesNoWinFour(t *testing.T) {
	var b Board
	row(t, &b, 0, 0, 4, Black)
	if b.WinFrom(3, 0) {
		t.Fatal("four in a row must not win")
	}
}

func TestRulesNoWinMixedColors(t *testing.T) {
	var b Board
	play(t, &b, 0, 0, Black)
	play(t, &b, 1, 0, Black)
	play(t, &b, 2, 0, White)
	play(t, &b, 3, 0, Black)
	play(t, &b, 4, 0, Black)
	if b.WinFrom(4, 0) {
		t.Fatal("interrupted run must not win")
	}
}

func TestRulesWinOverline(t *testing.T) {
	// Free-style: six in a row also wins (PROTOCOL §4, DECISIONS D2).
	var b Board
	row(t, &b, 9, 1, 6, White)
	if !b.WinFrom(3, 9) {
		t.Fatal("overline (6) must count as a win in free-style")
	}
}

func TestRulesWinCornersAndEdges(t *testing.T) {
	// A run anchored at the board corner.
	var b Board
	for i := 0; i < 5; i++ {
		play(t, &b, i, i, Black)
	}
	if !b.WinFrom(0, 0) {
		t.Fatal("five starting at corner (0,0) not detected")
	}

	// A run ending on the right edge.
	var b2 Board
	row(t, &b2, 14, 10, 5, White)
	if !b2.WinFrom(14, 14) {
		t.Fatal("five ending at edge (14,14) not detected")
	}

	// A diagonal touching top-right corner.
	var b3 Board
	for i := 0; i < 5; i++ {
		play(t, &b3, 14-i, 0+i, Black)
	}
	if !b3.WinFrom(10, 4) {
		t.Fatal("anti-diagonal to top-right corner not detected")
	}
}

func TestRulesIllegalMoves(t *testing.T) {
	var b Board
	if b.CanPlace(-1, 0) || b.CanPlace(0, -1) || b.CanPlace(15, 0) || b.CanPlace(0, 15) || b.CanPlace(99, 99) {
		t.Fatal("out-of-bounds cells must not be placeable")
	}
	play(t, &b, 7, 7, Black)
	if b.CanPlace(7, 7) {
		t.Fatal("occupied cell must not be placeable")
	}
	if b.WinFrom(0, 0) {
		t.Fatal("win check on empty cell must be false")
	}
}

func TestRulesFullBoardDraw(t *testing.T) {
	// Fill all 225 cells with a pattern that caps every directional run at 2
	// so the last stone cannot accidentally win: even rows use period-2
	// pairs, odd rows the inverted pattern. Black has 113 cells (odd move
	// numbers), white 112 — interleave them as the move sequence.
	pattern := func(x, y int) Color {
		v := (x / 2) % 2
		if y%2 == 1 {
			v = 1 - v
		}
		if v == 0 {
			return Black
		}
		return White
	}
	var blacks, whites [][2]int
	for y := 0; y < BoardSize; y++ {
		for x := 0; x < BoardSize; x++ {
			if pattern(x, y) == Black {
				blacks = append(blacks, [2]int{x, y})
			} else {
				whites = append(whites, [2]int{x, y})
			}
		}
	}
	if len(blacks) != 113 || len(whites) != 112 {
		t.Fatalf("pattern imbalance: black=%d white=%d", len(blacks), len(whites))
	}

	var b Board
	bi, wi := 0, 0
	for move := 0; move < BoardSize*BoardSize; move++ {
		var cell [2]int
		var c Color
		if move%2 == 0 { // black moves first (odd move numbers)
			cell, c, bi = blacks[bi], Black, bi+1
		} else {
			cell, c, wi = whites[wi], White, wi+1
		}
		play(t, &b, cell[0], cell[1], c)
		if b.WinFrom(cell[0], cell[1]) {
			t.Fatalf("move %d at %v unexpectedly won — pattern broken", move+1, cell)
		}
	}
	if !b.Full() {
		t.Fatal("225 moves must fill the board")
	}
}
