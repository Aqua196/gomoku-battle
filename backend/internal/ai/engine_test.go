package ai

import (
	"math/rand"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// place drops a stone at (x,y) — panics on misuse; test helper.
func place(b *game.Board, x, y int, c game.Color) {
	if !b.CanPlace(x, y) {
		panic("bad test placement")
	}
	b.Place(x, y, c)
}

func engine(difficulty string, think time.Duration) *Engine {
	cfg := For(difficulty)
	if think > 0 {
		cfg.Think = think
	}
	return NewEngine(cfg)
}

// TestAIWinInOne: one stone short of five in every direction -> the engine
// must take the winning cell.
func TestAIWinInOne(t *testing.T) {
	cases := []struct {
		name    string
		stones  [][2]int // AI (white) four, missing one
		opps    [][2]int
		winCell [2]int // one acceptable winning completion
		altCell [2]int // other acceptable completion
	}{
		{"horizontal", [][2]int{{5, 7}, {6, 7}, {7, 7}, {8, 7}}, [][2]int{{3, 3}}, [2]int{4, 7}, [2]int{9, 7}},
		{"vertical", [][2]int{{7, 5}, {7, 6}, {7, 7}, {7, 8}}, [][2]int{{11, 11}}, [2]int{7, 4}, [2]int{7, 9}},
		{"diag_main", [][2]int{{4, 4}, {5, 5}, {6, 6}, {7, 7}}, [][2]int{{0, 0}}, [2]int{3, 3}, [2]int{8, 8}},
		{"diag_anti", [][2]int{{8, 4}, {7, 5}, {6, 6}, {5, 7}}, [][2]int{{0, 0}}, [2]int{9, 3}, [2]int{4, 8}},
	}
	for _, diff := range []string{"easy", "normal", "hard"} {
		for _, tc := range cases {
			t.Run(diff+"/"+tc.name, func(t *testing.T) {
				var b game.Board
				for _, s := range tc.stones {
					place(&b, s[0], s[1], game.White)
				}
				for _, s := range tc.opps {
					place(&b, s[0], s[1], game.Black)
				}
				x, y, ok := engine(diff, 200*time.Millisecond).Move(&b, game.White, time.Now().Add(time.Second))
				if !ok {
					t.Fatal("no move")
				}
				if (x != tc.winCell[0] || y != tc.winCell[1]) && (x != tc.altCell[0] || y != tc.altCell[1]) {
					t.Fatalf("move (%d,%d) is not a winning completion", x, y)
				}
			})
		}
	}
}

// TestAIBlockOppFour: the opponent's closed four must be blocked — the only
// cell that completes its five.
func TestAIBlockOppFour(t *testing.T) {
	cases := []struct {
		name   string
		black  [][2]int // opponent (black) closed four
		white  [][2]int
		mustAt [2]int // the only blocking cell
	}{
		// X X X X . with left end blocked by edge -> only (4,7) works.
		{"horizontal_edge", [][2]int{{0, 7}, {1, 7}, {2, 7}, {3, 7}}, [][2]int{{10, 10}, {11, 11}, {12, 12}}, [2]int{4, 7}},
		// O X X X X . with the left end blocked by white -> only (7,5) works.
		{"horizontal_blocked", [][2]int{{3, 5}, {4, 5}, {5, 5}, {6, 5}}, [][2]int{{2, 5}, {2, 2}}, [2]int{7, 5}},
		{"vertical", [][2]int{{7, 3}, {7, 4}, {7, 5}, {7, 6}}, [][2]int{{7, 2}, {1, 1}}, [2]int{7, 7}},
		{"diag", [][2]int{{4, 4}, {5, 5}, {6, 6}, {7, 7}}, [][2]int{{3, 3}, {0, 14}}, [2]int{8, 8}},
	}
	for _, diff := range []string{"easy", "normal", "hard"} {
		for _, tc := range cases {
			t.Run(diff+"/"+tc.name, func(t *testing.T) {
				var b game.Board
				for _, s := range tc.black {
					place(&b, s[0], s[1], game.Black)
				}
				for _, s := range tc.white {
					place(&b, s[0], s[1], game.White)
				}
				x, y, ok := engine(diff, 300*time.Millisecond).Move(&b, game.White, time.Now().Add(time.Second))
				if !ok {
					t.Fatal("no move")
				}
				if x != tc.mustAt[0] || y != tc.mustAt[1] {
					t.Fatalf("move (%d,%d), want block at %v", x, y, tc.mustAt)
				}
			})
		}
	}
}

// TestAIOpenThreeAndOpenFour: the engine must seize an immediate open-four
// creation (.OOOO.) and must block an opponent's open three (.XXX.) before it
// becomes an unstoppable open four.
func TestAIOpenThreeAndOpenFour(t *testing.T) {
	t.Run("create_open_four", func(t *testing.T) {
		var b game.Board
		// White has open three at (5,7),(6,7),(7,7); (4,7) and (8,7) both create .OOOO.
		for _, s := range [][2]int{{5, 7}, {6, 7}, {7, 7}} {
			place(&b, s[0], s[1], game.White)
		}
		place(&b, 2, 2, game.Black)
		place(&b, 2, 3, game.Black)
		x, y, ok := engine("normal", 200*time.Millisecond).Move(&b, game.White, time.Now().Add(time.Second))
		if !ok {
			t.Fatal("no move")
		}
		if (x != 4 || y != 7) && (x != 8 || y != 7) {
			t.Fatalf("move (%d,%d) did not create open four at (4,7) or (8,7)", x, y)
		}
	})

	t.Run("block_opp_open_three", func(t *testing.T) {
		var b game.Board
		// Black has open three at (5,7),(6,7),(7,7) with (4,7) and (8,7) open.
		for _, s := range [][2]int{{5, 7}, {6, 7}, {7, 7}} {
			place(&b, s[0], s[1], game.Black)
		}
		place(&b, 7, 6, game.White)
		place(&b, 10, 10, game.White)
		x, y, ok := engine("normal", 200*time.Millisecond).Move(&b, game.White, time.Now().Add(time.Second))
		if !ok {
			t.Fatal("no move")
		}
		if (x != 4 || y != 7) && (x != 8 || y != 7) {
			t.Fatalf("move (%d,%d) did not block open three at (4,7) or (8,7)", x, y)
		}
	})
}

// TestAITimeLimit: a busy mid-game position must return within the budget.
func TestAITimeLimit(t *testing.T) {
	var b game.Board
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 60; i++ { // dense, ambiguous mid-game
		for {
			x, y := r.Intn(game.BoardSize), r.Intn(game.BoardSize)
			if b.CanPlace(x, y) {
				b.Place(x, y, game.Color(i%2+1))
				break
			}
		}
	}
	start := time.Now()
	_, _, ok := engine("hard", 0).Move(&b, game.White, time.Now().Add(1800*time.Millisecond))
	if !ok {
		t.Fatal("no move")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("hard move took %v > 2s", el)
	}
}

// TestAIvsRandom100: hard beats a random-move baseline in >=95% of games.
// AI plays white (its production seat); the bench uses a 12ms budget so the
// suite stays fast — the engine still plays wins/blocks instantly.
func TestAIvsRandom100(t *testing.T) {
	cfg := For("hard")
	cfg.Think = 12 * time.Millisecond
	eng := NewEngine(cfg)
	r := rand.New(rand.NewSource(42))
	wins, losses, draws := 0, 0, 0

	for g := 0; g < 100; g++ {
		var b game.Board
		turn := game.Black // random plays black, moves first
		deadlineMoves := 0
		for {
			var x, y int
			if turn == game.Black {
				// random legal move near stones
				for {
					x, y = r.Intn(game.BoardSize), r.Intn(game.BoardSize)
					if b.CanPlace(x, y) {
						break
					}
				}
			} else {
				var ok bool
				x, y, ok = eng.Move(&b, game.White, time.Now().Add(cfg.Think))
				if !ok {
					draws++
					break // board full, no cell left
				}
				deadlineMoves++
			}
			b.Place(x, y, turn)
			if b.WinFrom(x, y) {
				if turn == game.White {
					wins++
				} else {
					losses++
				}
				break
			}
			if b.Full() {
				draws++
				break
			}
			turn = turn.Other()
		}
	}
	t.Logf("AI vs random: wins=%d losses=%d draws=%d", wins, losses, draws)
	if wins < 95 {
		t.Fatalf("AI win rate %d%% < 95%% (losses=%d draws=%d)", wins, losses, draws)
	}
}
