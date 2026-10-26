// Contract tests: one table-driven suite exercised against every Store
// implementation — MemStore and RedisStore (via miniredis, an in-process
// Redis protocol server, no external dependency). A new implementation
// only has to pass storeContract to satisfy the interface's semantics.
package store

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestStoreContractMem(t *testing.T) {
	storeContract(t, "mem", func(t *testing.T) Store { return NewMemStore() })
}

func TestRedisStoreContract(t *testing.T) {
	storeContract(t, "redis", func(t *testing.T) Store {
		mr := miniredis.RunT(t)
		st, err := NewRedisStore(mr.Addr())
		if err != nil {
			t.Fatalf("NewRedisStore: %v", err)
		}
		return st
	})
}

// storeContract runs the shared Store semantics suite. fresh() returns a
// new empty store each call so sub-scenarios stay isolated.
func storeContract(t *testing.T, name string, fresh func(t *testing.T) Store) {
	t.Run(name+"/player_lifecycle", func(t *testing.T) {
		s := fresh(t)
		rec := s.CreatePlayer("alice")
		if rec == nil || rec.PlayerID == "" || rec.Token == "" || rec.Nick != "alice" {
			t.Fatalf("CreatePlayer returned %+v", rec)
		}
		byTok, ok := s.PlayerByToken(rec.Token)
		if !ok || byTok.PlayerID != rec.PlayerID {
			t.Fatalf("PlayerByToken miss: %v %v", byTok, ok)
		}
		byID, ok := s.PlayerByID(rec.PlayerID)
		if !ok || byID.Token != rec.Token {
			t.Fatalf("PlayerByID miss: %v %v", byID, ok)
		}
		s.UpdateNick(rec.PlayerID, "alice2")
		if got, _ := s.PlayerByID(rec.PlayerID); got.Nick != "alice2" {
			t.Fatalf("UpdateNick not persisted: %+v", got)
		}
		s.DeletePlayer(rec.PlayerID)
		if _, ok := s.PlayerByID(rec.PlayerID); ok {
			t.Fatal("PlayerByID hit after DeletePlayer")
		}
		if _, ok := s.PlayerByToken(rec.Token); ok {
			t.Fatal("PlayerByToken hit after DeletePlayer")
		}
	})

	t.Run(name+"/player_misses", func(t *testing.T) {
		s := fresh(t)
		if _, ok := s.PlayerByToken("nope"); ok {
			t.Fatal("phantom token hit")
		}
		if _, ok := s.PlayerByID("p_nope"); ok {
			t.Fatal("phantom id hit")
		}
		s.DeletePlayer("p_nope") // must not panic
		s.UpdateNick("p_nope", "x")
	})

	t.Run(name+"/token_uniqueness", func(t *testing.T) {
		s := fresh(t)
		seen := map[string]bool{}
		for i := 0; i < 64; i++ {
			rec := s.CreatePlayer("n")
			if seen[rec.Token] {
				t.Fatalf("duplicate token %q", rec.Token)
			}
			seen[rec.Token] = true
		}
	})
}

// RedisStore-only: a killed backend is invisible to other clients — the
// store must not serve it. Prove the client degrades to miss, not panic.
func TestRedisStoreDegradesOnDown(t *testing.T) {
	mr := miniredis.RunT(t)
	st, err := NewRedisStore(mr.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	rec := st.CreatePlayer("up")
	mr.Close()
	if _, ok := st.PlayerByToken(rec.Token); ok {
		t.Fatal("hit while redis down")
	}
	if got := st.RecentGames(10); len(got) != 0 {
		t.Fatalf("RecentGames while down: %+v", got)
	}
	st.DeletePlayer("p_x")
	st.UpdateNick("p_x", "y")
	st.SaveGame(GameRecord{ID: "r_1"})
}

// RedisStore-only: NewRedisStore refuses to hand back a dead client so the
// caller can fall back cleanly.
func TestRedisStoreDialFail(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	mr.Close()
	if _, err := NewRedisStore(addr); err == nil {
		t.Fatal("expected dial failure")
	}
}

// RedisStore-only: reads slide the player TTL forward, so a player active
// near the 24h edge is not evicted mid-session.
func TestRedisStoreSlidingTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	st, err := NewRedisStore(mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	rec := st.CreatePlayer("alice")
	mr.FastForward(23 * time.Hour)
	if _, ok := st.PlayerByID(rec.PlayerID); !ok {
		t.Fatal("player missing before TTL")
	}
	mr.FastForward(2 * time.Hour) // 25h since write, ~2h since sliding read
	if _, ok := st.PlayerByID(rec.PlayerID); !ok {
		t.Fatal("player evicted despite sliding TTL")
	}
}

// RedisStore-only: every call is bounded by opTimeout; a dead Redis must
// not hang the caller (the hub lock it runs under) for seconds.
func TestRedisStoreBoundedWhenDown(t *testing.T) {
	mr := miniredis.RunT(t)
	st, err := NewRedisStore(mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	rec := st.CreatePlayer("bob")
	mr.Close()
	start := time.Now()
	st.PlayerByID(rec.PlayerID)
	st.SaveGame(GameRecord{ID: "x"})
	st.RecentGames(10)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("degraded calls took %v (unbounded freeze)", d)
	}
}
