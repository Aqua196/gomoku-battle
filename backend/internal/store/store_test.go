// Contract tests: one table-driven suite exercised against every Store
// implementation — MemStore and RedisStore (via miniredis, an in-process
// Redis protocol server, no external dependency). A new implementation
// only has to pass storeContract to satisfy the interface's semantics.
package store

import (
	"sort"
	"testing"

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

	t.Run(name+"/room_registry", func(t *testing.T) {
		s := fresh(t)
		r1 := RoomRecord{ID: "r_1", Name: "a", Status: "waiting", CreatedAt: 1}
		r2 := RoomRecord{ID: "r_2", Name: "b", Status: "playing", IsAI: true, CreatedAt: 2}
		s.UpsertRoom(r1)
		s.UpsertRoom(r2)
		got, ok := s.RoomByID("r_1")
		if !ok || got.Name != "a" || got.Status != "waiting" {
			t.Fatalf("RoomByID: %+v %v", got, ok)
		}
		if _, ok := s.RoomByID("r_nope"); ok {
			t.Fatal("phantom room hit")
		}
		// Upsert replaces, not duplicates.
		r1.Status = "playing"
		s.UpsertRoom(r1)
		if got, _ := s.RoomByID("r_1"); got.Status != "playing" {
			t.Fatalf("upsert did not replace: %+v", got)
		}
		ids := []string{}
		for _, r := range s.ListRooms() {
			ids = append(ids, r.ID)
		}
		sort.Strings(ids)
		if len(ids) != 2 || ids[0] != "r_1" || ids[1] != "r_2" {
			t.Fatalf("ListRooms: %v", ids)
		}
		s.DeleteRoom("r_1")
		if _, ok := s.RoomByID("r_1"); ok {
			t.Fatal("RoomByID hit after DeleteRoom")
		}
		if got := s.ListRooms(); len(got) != 1 || got[0].ID != "r_2" {
			t.Fatalf("ListRooms after delete: %+v", got)
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
	if _, ok := st.RoomByID("r_1"); ok {
		t.Fatal("room hit while redis down")
	}
	if got := st.ListRooms(); len(got) != 0 {
		t.Fatalf("ListRooms while down: %+v", got)
	}
	st.DeletePlayer("p_x")
	st.DeleteRoom("r_x")
	st.UpdateNick("p_x", "y")
	st.UpsertRoom(RoomRecord{ID: "r_1"})
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
