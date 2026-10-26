// Aiarena plays the current AI engine against an opponent — by default
// the frozen v1 baseline (internal/ai/baseline), or another preset of the
// current engine — and reports wins, losses and search statistics. Each
// random opening is played twice with colours swapped, so neither side
// profits from the first-move advantage of free-style gomoku.
//
//	go run ./cmd/aiarena -openings 20 -think 300ms -a hard -b v1:hard
//	go run ./cmd/aiarena -a hard -b normal   # current hard vs current normal
package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/ai"
	"github.com/Aqua196/gomoku-battle/backend/internal/ai/baseline"
	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

type player interface {
	Move(b *game.Board, me game.Color, deadline time.Time) (int, int, bool)
}

type result struct {
	opening, game int
	newColor      game.Color
	winner        game.Color // Empty for a draw
	moves         int
	newMoves      int
	depthSum      int
	nodesSum      int
	vcfWins       int
	newTime       time.Duration
	oldTime       time.Duration
	oldMoves      int
	log           []string // per-move trace, kept for -trace
}

func main() {
	openings := flag.Int("openings", 20, "random openings; each is played twice with colours swapped")
	think := flag.Duration("think", 300*time.Millisecond, "per-move budget for both engines")
	a := flag.String("a", "hard", "difficulty preset of the current engine")
	b := flag.String("b", "v1:hard", "opponent: v1:<preset> for the baseline engine, <preset> for the current one")
	parallel := flag.Int("parallel", 2, "games played at once (keep below the core count: search is time-bound)")
	seed := flag.Uint64("seed", 1, "opening generator seed")
	trace := flag.Bool("trace", false, "print the move-by-move trace of every game the current engine loses")
	flag.Parse()

	jobs := make(chan [2]int)
	results := make(chan result)
	var wg sync.WaitGroup
	for range *parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results <- play(j[0], j[1], opening(*seed, j[0]), *a, *b, *think)
			}
		}()
	}
	go func() {
		for i := range *openings {
			jobs <- [2]int{i, 0}
			jobs <- [2]int{i, 1}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var win, loss, draw, newMoves, depth, nodes, vcf, oldMoves int
	var newTime, oldTime time.Duration
	for r := range results {
		outcome := "draw"
		switch r.winner {
		case r.newColor:
			win++
			outcome = "new wins"
		case game.Empty:
			draw++
		default:
			loss++
			outcome = "opponent wins"
		}
		fmt.Printf("opening %2d game %d  new=%-5s  %3d moves  %s\n",
			r.opening, r.game, r.newColor.Name(), r.moves, outcome)
		if *trace && outcome == "opponent wins" {
			for _, l := range r.log {
				fmt.Println("   ", l)
			}
		}
		newMoves += r.newMoves
		depth += r.depthSum
		nodes += r.nodesSum
		vcf += r.vcfWins
		newTime += r.newTime
		oldTime += r.oldTime
		oldMoves += r.oldMoves
	}
	games := win + loss + draw
	fmt.Printf("\ncurrent %s vs %s, %d games, think %v\n", *a, *b, games, *think)
	fmt.Printf("current: %d wins, %d losses, %d draws (%.0f%%)\n", win, loss, draw, 100*float64(win)/float64(max(1, games)))
	if newMoves > 0 {
		fmt.Printf("current per searched move: depth %.1f, nodes %d, %v; moves decided by VCF: %d\n",
			float64(depth)/float64(newMoves), nodes/newMoves, newTime/time.Duration(newMoves), vcf)
	}
	if oldMoves > 0 {
		fmt.Printf("opponent per move: %v\n", oldTime/time.Duration(oldMoves))
	}
	if loss > win {
		os.Exit(1)
	}
}

// opening places three stones (black, white, black) near the centre, so
// white moves first after it.
func opening(seed uint64, i int) [][2]int {
	r := rand.New(rand.NewPCG(seed, uint64(i)+1)) //nolint:gosec // i >= 0
	var out [][2]int
	used := map[[2]int]bool{}
	for len(out) < 3 {
		c := [2]int{5 + r.IntN(5), 5 + r.IntN(5)}
		if !used[c] {
			used[c] = true
			out = append(out, c)
		}
	}
	return out
}

func play(op, g int, stones [][2]int, a, b string, think time.Duration) result {
	var board game.Board
	turn := game.Black
	for _, s := range stones {
		board.Place(s[0], s[1], turn)
		turn = turn.Other()
	}
	cur := ai.NewEngine(ai.For(a))
	var old player
	if preset, ok := strings.CutPrefix(b, "v1:"); ok {
		old = baseline.NewEngine(baseline.For(preset))
	} else {
		old = ai.NewEngine(ai.For(b))
	}
	res := result{opening: op, game: g, newColor: game.White}
	if g == 1 {
		res.newColor = game.Black
	}
	for res.moves = len(stones); res.moves < game.BoardSize*game.BoardSize; res.moves++ {
		p := old
		if turn == res.newColor {
			p = cur
		}
		start := time.Now()
		x, y, ok := p.Move(&board, turn, start.Add(think))
		took := time.Since(start)
		if !ok {
			return res
		}
		who := "opponent"
		if turn == res.newColor {
			who = fmt.Sprintf("current (%s d%d)", cur.Stats().Reason, cur.Stats().Depth)
		}
		res.log = append(res.log, fmt.Sprintf("%3d %-5s %c%-2d %s", res.moves+1, turn.Name(), 'A'+x, game.BoardSize-y, who))
		if turn == res.newColor {
			st := cur.Stats()
			res.newTime += took
			if st.Reason == "search" {
				res.newMoves++
				res.depthSum += st.Depth
				res.nodesSum += st.Nodes
			}
			if st.Reason == "vcf" {
				res.vcfWins++
			}
		} else {
			res.oldTime += took
			res.oldMoves++
		}
		if !board.CanPlace(x, y) {
			panic(fmt.Sprintf("illegal move (%d,%d) by %s", x, y, turn.Name()))
		}
		board.Place(x, y, turn)
		if board.WinFrom(x, y) {
			res.winner = turn
			res.moves++
			return res
		}
		turn = turn.Other()
	}
	return res
}
