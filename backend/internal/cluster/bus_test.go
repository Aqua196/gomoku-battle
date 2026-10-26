package cluster

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestMemBusOrderAndBroadcast(t *testing.T) {
	net := NewMemNetwork()
	a, b, c := net.Join("a"), net.Join("b"), net.Join("c")
	busContract(t, a, b, c)
	net.Leave("b")
	a.Send("b", Envelope{Kind: KindDeliver}) // dead node: dropped, no panic
}

func TestRedisBusOrderAndBroadcast(t *testing.T) {
	mr := miniredis.RunT(t)
	client := func() *redis.Client {
		c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	a, b, c := NewRedisBus(client(), "a"), NewRedisBus(client(), "b"), NewRedisBus(client(), "c")
	t.Cleanup(func() { a.Close(); b.Close(); c.Close() })
	busContract(t, a, b, c)
}

// busContract: 200 envelopes a→b arrive in order with the sender stamped;
// a broadcast from a reaches b and c but not a.
func busContract(t *testing.T, a, b, c Bus) {
	t.Helper()
	inbox := map[string]chan Envelope{"a": make(chan Envelope, 512), "b": make(chan Envelope, 512), "c": make(chan Envelope, 512)}
	a.Start(func(e Envelope) { inbox["a"] <- e })
	b.Start(func(e Envelope) { inbox["b"] <- e })
	c.Start(func(e Envelope) { inbox["c"] <- e })

	for i := range 200 {
		a.Send("b", Envelope{Kind: KindMove, X: i})
	}
	for i := range 200 {
		select {
		case e := <-inbox["b"]:
			if e.X != i || e.From != "a" || e.Kind != KindMove {
				t.Fatalf("envelope %d: %+v", i, e)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("envelope %d never arrived", i)
		}
	}

	a.Broadcast(Envelope{Kind: KindLobby})
	for _, n := range []string{"b", "c"} {
		select {
		case e := <-inbox[n]:
			if e.Kind != KindLobby {
				t.Fatalf("%s got %+v", n, e)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("broadcast missed %s", n)
		}
	}
	select {
	case e := <-inbox["a"]:
		t.Fatalf("broadcast echoed to its sender: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}
