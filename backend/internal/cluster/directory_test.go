package cluster

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// One contract, both implementations (Redis via miniredis, in-process).

func TestMemDirectory(t *testing.T) {
	directoryContract(t, func(t *testing.T) (Directory, func(time.Duration)) {
		d := NewMemDirectory()
		var skew time.Duration
		d.now = func() time.Time { return time.Now().Add(skew) }
		return d, func(by time.Duration) { skew += by }
	})
}

func TestRedisDirectory(t *testing.T) {
	directoryContract(t, func(t *testing.T) (Directory, func(time.Duration)) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		return NewRedisDirectory(rdb), mr.FastForward
	})
}

func seat(pid string) SeatInfo { return SeatInfo{PlayerID: pid, Nick: pid + "-nick", Connected: true} }

func waiting(id, owner, creator string) RoomInfo {
	s := seat(creator)
	return RoomInfo{ID: id, Name: id, Status: StatusWaiting, Owner: owner, CreatedAt: 1, Seats: [2]*SeatInfo{&s}}
}

func directoryContract(t *testing.T, fresh func(*testing.T) (Directory, func(time.Duration))) {
	t.Run("location_epochs", func(t *testing.T) {
		d, _ := fresh(t)
		e1, prev := d.SetLocation("p", "n1")
		if prev != "" {
			t.Fatalf("first location has prev %q", prev)
		}
		e2, prev := d.SetLocation("p", "n2")
		if e2 <= e1 || prev != "n1" {
			t.Fatalf("epochs %d -> %d, prev %q", e1, e2, prev)
		}
		d.ClearLocation("p", "n1", e1) // stale socket closing: must not erase n2
		if n, ok := d.Location("p"); !ok || n != "n2" {
			t.Fatalf("location after stale clear: %q %v", n, ok)
		}
		d.ClearLocation("p", "n2", e2)
		if _, ok := d.Location("p"); ok {
			t.Fatal("location survived its own clear")
		}
	})

	t.Run("binding", func(t *testing.T) {
		d, _ := fresh(t)
		if !d.BindPlayer("p", "r1") || d.BindPlayer("p", "r2") {
			t.Fatal("a player must be in at most one room")
		}
		d.UnbindPlayer("p", "r2") // wrong room: no effect
		if r, _ := d.PlayerRoom("p"); r != "r1" {
			t.Fatalf("bound to %q", r)
		}
		d.UnbindPlayer("p", "r1")
		if _, ok := d.PlayerRoom("p"); ok {
			t.Fatal("still bound")
		}
	})

	t.Run("rooms_and_claim", func(t *testing.T) {
		d, _ := fresh(t)
		d.BindPlayer("a", "r1")
		d.PutRoom(waiting("r1", "n1", "a"))
		if rs := d.Rooms(); len(rs) != 1 || rs[0].Seats[0].PlayerID != "a" {
			t.Fatalf("rooms: %+v", rs)
		}
		d.BindPlayer("busy", "elsewhere")
		if _, err := d.ClaimSeat("r1", seat("busy")); !errors.Is(err, ErrBound) {
			t.Fatalf("claim by a bound player: %v", err)
		}
		r, err := d.ClaimSeat("r1", seat("b"))
		if err != nil || r.Status != StatusPlaying || r.Seats[1].PlayerID != "b" {
			t.Fatalf("claim: %+v %v", r, err)
		}
		if room, _ := d.PlayerRoom("b"); room != "r1" {
			t.Fatal("claim must bind the joiner")
		}
		if _, err := d.ClaimSeat("r1", seat("c")); !errors.Is(err, ErrRoomFull) {
			t.Fatalf("second claim: %v", err)
		}
		if _, err := d.ClaimSeat("nope", seat("c")); !errors.Is(err, ErrNoRoom) {
			t.Fatalf("claim missing room: %v", err)
		}
		if d.CloseWaiting("r1") {
			t.Fatal("a playing room cannot be closed as waiting")
		}
		got, _ := d.Room("r1")
		got.Seats[0].Nick = "mutated" // callers own their copy
		if again, _ := d.Room("r1"); again.Seats[0].Nick == "mutated" {
			t.Fatal("Room shares seat pointers")
		}
		d.DeleteRoom("r1")
		if _, ok := d.Room("r1"); ok || len(d.Rooms()) != 0 {
			t.Fatal("room survived delete")
		}
	})

	t.Run("close_waiting", func(t *testing.T) {
		d, _ := fresh(t)
		d.BindPlayer("a", "r1")
		d.PutRoom(waiting("r1", "n1", "a"))
		if !d.CloseWaiting("r1") {
			t.Fatal("close waiting failed")
		}
		if _, ok := d.PlayerRoom("a"); ok {
			t.Fatal("creator still bound after close")
		}
		if d.CloseWaiting("r1") {
			t.Fatal("closed twice")
		}
	})

	t.Run("queue_pairs_fifo", func(t *testing.T) {
		d, _ := fresh(t)
		if pair, pos := d.Enqueue(QueueEntry{PlayerID: "a", Node: "n1"}, "r1"); pair != nil || pos != 1 {
			t.Fatalf("first enqueue: %v %d", pair, pos)
		}
		if _, pos := d.Enqueue(QueueEntry{PlayerID: "a", Node: "n1"}, "r1"); pos != 1 {
			t.Fatalf("re-enqueue moved the player: %d", pos)
		}
		pair, _ := d.Enqueue(QueueEntry{PlayerID: "b", Node: "n2"}, "r1")
		if len(pair) != 2 || pair[0].PlayerID != "a" || pair[1].PlayerID != "b" || pair[1].Node != "n2" {
			t.Fatalf("pair: %+v", pair)
		}
		for _, p := range []string{"a", "b"} {
			if r, _ := d.PlayerRoom(p); r != "r1" {
				t.Fatalf("%s not bound to the new room", p)
			}
		}
		if len(d.Queue()) != 0 {
			t.Fatal("paired players left in the queue")
		}
	})

	t.Run("queue_skips_bound_and_dequeues", func(t *testing.T) {
		d, _ := fresh(t)
		d.Enqueue(QueueEntry{PlayerID: "a"}, "rX")
		d.BindPlayer("a", "r-other") // a joined a room some other way
		if pair, pos := d.Enqueue(QueueEntry{PlayerID: "b"}, "r2"); pair != nil || pos != 1 {
			t.Fatalf("stale entry was paired: %v %d", pair, pos)
		}
		if !d.Dequeue("b") || d.Dequeue("b") {
			t.Fatal("dequeue must report presence exactly once")
		}
	})

	t.Run("queue_concurrent_pairs_once", func(t *testing.T) {
		d, _ := fresh(t)
		var mu sync.Mutex
		paired := map[string]int{}
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pair, _ := d.Enqueue(QueueEntry{PlayerID: fmt.Sprintf("p%02d", i)}, fmt.Sprintf("r%02d", i))
				mu.Lock()
				for _, q := range pair {
					paired[q.PlayerID]++
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		if len(paired)+len(d.Queue()) != 20 {
			t.Fatalf("players lost or duplicated: paired %d + queued %d", len(paired), len(d.Queue()))
		}
		for p, n := range paired {
			if n != 1 {
				t.Fatalf("%s paired %d times", p, n)
			}
		}
	})

	t.Run("live_games", func(t *testing.T) {
		d, _ := fresh(t)
		g := LiveGame{RoomID: "r1", Seats: [2]SeatInfo{seat("a"), seat("b")}, Moves: [][2]int{{7, 7}}, AIDifficulty: ""}
		d.SaveLive(g)
		g.Moves = append(g.Moves, [2]int{8, 8})
		d.SaveLive(g)
		gs := d.LiveGames()
		if len(gs) != 1 || len(gs[0].Moves) != 2 {
			t.Fatalf("live games: %+v", gs)
		}
		d.DeleteLive("r1")
		if len(d.LiveGames()) != 0 {
			t.Fatal("live game survived delete")
		}
	})

	t.Run("leases", func(t *testing.T) {
		d, advance := fresh(t)
		ttl := 2 * time.Second
		if !d.AcquireLease("r1", "n1", ttl) || d.AcquireLease("r1", "n2", ttl) {
			t.Fatal("lease must be exclusive")
		}
		if !d.AcquireLease("r1", "n1", ttl) {
			t.Fatal("holder may re-acquire")
		}
		if !d.RenewLease("r1", "n1", ttl) || d.RenewLease("r1", "n2", ttl) {
			t.Fatal("only the holder renews")
		}
		advance(3 * time.Second)
		if _, held := d.LeaseHolder("r1"); held {
			t.Fatal("lease outlived its ttl")
		}
		if d.RenewLease("r1", "n1", ttl) {
			t.Fatal("an expired lease cannot be renewed")
		}
		if !d.AcquireLease("r1", "n2", ttl) {
			t.Fatal("an expired lease is up for grabs")
		}
		d.ReleaseLease("r1", "n1") // not the holder: no effect
		if h, _ := d.LeaseHolder("r1"); h != "n2" {
			t.Fatalf("holder %q", h)
		}
		d.ReleaseLease("r1", "n2")
		if _, held := d.LeaseHolder("r1"); held {
			t.Fatal("released lease still held")
		}
	})

	t.Run("heartbeats", func(t *testing.T) {
		d, advance := fresh(t)
		d.Heartbeat("n1", time.Second)
		if !d.Alive("n1") || d.Alive("n2") {
			t.Fatal("alive")
		}
		advance(2 * time.Second)
		if d.Alive("n1") {
			t.Fatal("heartbeat outlived its ttl")
		}
	})
}
