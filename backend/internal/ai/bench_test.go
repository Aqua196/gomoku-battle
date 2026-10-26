package ai

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/ai/baseline"
	"github.com/Aqua196/gomoku-battle/backend/internal/game"
)

// midgame builds reproducible, decided-free mid-game positions.
func midgame(n int) []game.Board {
	r := rand.New(rand.NewPCG(2026, 10))
	var out []game.Board
	for len(out) < n {
		var b game.Board
		for i := 0; i < 24; i++ {
			x, y := 4+r.IntN(7), 4+r.IntN(7)
			if b[y][x] == game.Empty {
				b[y][x] = game.Color(1 + i%2)
			}
		}
		var p position
		p.load(&b)
		if !anyFive(&b) && !p.hasFive(game.Black) && !p.hasFive(game.White) {
			out = append(out, b)
		}
	}
	return out
}

// Evaluating after a move: rescoring the 4 touched lines vs rebuilding the
// whole board's line table (what a non-incremental evaluator pays).
func BenchmarkEvalIncremental(b *testing.B) {
	pos := midgame(1)[0]
	var p position
	p.load(&pos)
	sink := 0
	for b.Loop() {
		p.place(2, 2, game.Black)
		sink += p.eval(game.White)
		p.remove(2, 2)
	}
	_ = sink
}

func BenchmarkEvalRebuild(b *testing.B) {
	pos := midgame(1)[0]
	pos[2][2] = game.Black
	var p position
	sink := 0
	for b.Loop() {
		p.load(&pos)
		sink += p.eval(game.White)
	}
	_ = sink
}

// Fixed-depth searches on the same positions: time per search and nodes.
func BenchmarkSearchDepth6(b *testing.B) {
	boards := midgame(4)
	cfg := For("hard")
	cfg.MaxDepth, cfg.Think, cfg.VCFDepth, cfg.VCTDepth, cfg.Defend = 6, time.Hour, 0, 0, false
	nodes := 0
	for b.Loop() {
		for i := range boards {
			e := NewEngine(cfg)
			e.Move(&boards[i], game.White, time.Now().Add(time.Hour))
			nodes += e.Stats().Nodes
		}
	}
	b.ReportMetric(float64(nodes)/float64(b.N)/float64(len(boards)), "nodes/search")
}

func BenchmarkBaselineDepth4(b *testing.B) {
	boards := midgame(4)
	cfg := baseline.For("normal") // fixed depth 4, breadth 10
	cfg.Think = time.Hour
	for b.Loop() {
		for i := range boards {
			e := baseline.NewEngine(cfg)
			e.Move(&boards[i], game.White, time.Now().Add(time.Hour))
		}
	}
}

func BenchmarkSearchDepth4(b *testing.B) {
	boards := midgame(4)
	cfg := For("hard")
	cfg.MaxDepth, cfg.Breadth, cfg.Think, cfg.VCFDepth, cfg.VCTDepth, cfg.Defend = 4, 10, time.Hour, 0, 0, false
	for b.Loop() {
		for i := range boards {
			e := NewEngine(cfg)
			e.Move(&boards[i], game.White, time.Now().Add(time.Hour))
		}
	}
}
