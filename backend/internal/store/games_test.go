package store

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestGameRecordsMem(t *testing.T) {
	gameContract(t, func(t *testing.T) Store { return NewMemStore() })
}

func TestGameRecordsRedis(t *testing.T) {
	gameContract(t, func(t *testing.T) Store {
		st, err := NewRedisStore(miniredis.RunT(t).Addr())
		if err != nil {
			t.Fatal(err)
		}
		return st
	})
}

func game(i int, black, white string) GameRecord {
	return GameRecord{
		ID:        fmt.Sprintf("r_%03d", i),
		Black:     SeatRecord{PlayerID: black, Nick: black + "-nick"},
		White:     SeatRecord{PlayerID: white, Nick: white + "-nick", IsAI: white == "ai"},
		Moves:     [][2]int{{7, 7}, {8, 8}, {7, 8}},
		Winner:    "black",
		Reason:    "resign",
		StartedAt: int64(1000 + i),
		EndedAt:   int64(2000 + i),
	}
}

func gameContract(t *testing.T, fresh func(t *testing.T) Store) {
	t.Run("roundtrip", func(t *testing.T) {
		s := fresh(t)
		g := game(1, "p_a", "p_b")
		s.SaveGame(g)
		got, ok := s.GameByID(g.ID)
		if !ok || !reflect.DeepEqual(got, g) {
			t.Fatalf("GameByID = %+v %v, want %+v", got, ok, g)
		}
		got.Moves[0] = [2]int{0, 0} // callers own their copy
		if again, _ := s.GameByID(g.ID); again.Moves[0] != [2]int{7, 7} {
			t.Fatal("record shares its moves slice with callers")
		}
		if _, ok := s.GameByID("r_missing"); ok {
			t.Fatal("hit for a missing game")
		}
	})

	t.Run("indexes_newest_first", func(t *testing.T) {
		s := fresh(t)
		s.SaveGame(game(1, "p_a", "p_b"))
		s.SaveGame(game(2, "p_a", "ai"))
		s.SaveGame(game(3, "p_c", "p_b"))
		ids := func(gs []GameRecord) (out []string) {
			for _, g := range gs {
				out = append(out, g.ID)
			}
			return out
		}
		if got := ids(s.RecentGames(10)); !reflect.DeepEqual(got, []string{"r_003", "r_002", "r_001"}) {
			t.Fatalf("recent = %v", got)
		}
		if got := ids(s.RecentGames(2)); !reflect.DeepEqual(got, []string{"r_003", "r_002"}) {
			t.Fatalf("recent limit = %v", got)
		}
		if got := ids(s.PlayerGames("p_a", 10)); !reflect.DeepEqual(got, []string{"r_002", "r_001"}) {
			t.Fatalf("p_a games = %v", got)
		}
		if got := ids(s.PlayerGames("p_b", 10)); !reflect.DeepEqual(got, []string{"r_003", "r_001"}) {
			t.Fatalf("p_b games = %v", got)
		}
		if got := s.PlayerGames("ai", 10); len(got) != 0 {
			t.Fatalf("AI seats must not be indexed: %v", ids(got))
		}
	})

	t.Run("retention_caps", func(t *testing.T) {
		s := fresh(t)
		for i := 0; i < MaxRecentGames+5; i++ {
			s.SaveGame(game(i, "p_a", fmt.Sprintf("p_%d", i)))
		}
		if got := len(s.RecentGames(1000)); got != MaxRecentGames {
			t.Fatalf("recent holds %d, want %d", got, MaxRecentGames)
		}
		mine := s.PlayerGames("p_a", 1000)
		if len(mine) != MaxPlayerGames || mine[0].ID != fmt.Sprintf("r_%03d", MaxRecentGames+4) {
			t.Fatalf("player list: %d entries, newest %s", len(mine), mine[0].ID)
		}
	})
}
