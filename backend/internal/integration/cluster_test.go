package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/cluster"
	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/Aqua196/gomoku-battle/backend/internal/store"
	"github.com/Aqua196/gomoku-battle/backend/internal/ws"
	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Multi-node scenarios (DESIGN §10). Every test runs twice: nodes sharing
// an in-process directory and bus, and nodes sharing one Redis (miniredis)
// for store, directory and pub/sub — the production wiring.

func clusterCfg() protocol.Config {
	cfg := fastCfg()
	cfg.MoveTimeout = 5 * time.Second
	cfg.ReconnectWindow = 3 * time.Second
	cfg.AbandonTTL = 10 * time.Second
	cfg.LeaseTTL = 400 * time.Millisecond
	cfg.RecoverInterval = 50 * time.Millisecond
	cfg.AIThink = 30 * time.Millisecond
	return cfg
}

// testCluster is a set of nodes sharing state; nodes can be added (a
// restart) and killed.
type testCluster struct {
	t     *testing.T
	cfg   protocol.Config
	st    store.Store
	dir   cluster.Directory
	bus   func(node string) cluster.Bus
	nodes []*testNode
	// expire lets leases and heartbeats lapse: miniredis keeps TTLs frozen
	// until fast-forwarded; the in-memory directory runs on the real clock.
	expire func(time.Duration)
}

type testNode struct {
	hub *hub.Hub
	ts  *httptest.Server
}

func eachBackend(t *testing.T, run func(t *testing.T, c *testCluster)) {
	t.Run("mem", func(t *testing.T) {
		net := cluster.NewMemNetwork()
		run(t, &testCluster{t: t, cfg: clusterCfg(), st: store.NewMemStore(), dir: cluster.NewMemDirectory(),
			bus: func(node string) cluster.Bus { return net.Join(node) }, expire: time.Sleep})
	})
	t.Run("redis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := func() *redis.Client {
			c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = c.Close() })
			return c
		}
		st, err := store.NewRedisStore(mr.Addr())
		if err != nil {
			t.Fatal(err)
		}
		run(t, &testCluster{t: t, cfg: clusterCfg(), st: st, dir: cluster.NewRedisDirectory(client()),
			bus: func(node string) cluster.Bus { return cluster.NewRedisBus(client(), node) }, expire: mr.FastForward})
	})
}

func (c *testCluster) add() *testNode {
	id := fmt.Sprintf("node%d", len(c.nodes))
	h := hub.NewNode(c.st, c.cfg, hub.Options{Node: id, Dir: c.dir, Bus: c.bus(id)})
	n := &testNode{hub: h, ts: httptest.NewServer(ws.NewServer(h, c.cfg, true).Handler("", false))}
	c.t.Cleanup(func() {
		n.ts.Close()
		h.Close()
	})
	c.nodes = append(c.nodes, n)
	return n
}

// kill crashes a node: sockets drop, games stop with no cleanup.
func (n *testNode) kill() {
	n.ts.CloseClientConnections()
	n.hub.Kill()
}

func waitFrame(t *testing.T, c *wsclient.Client, typ string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m, err := c.WaitType(typ, time.Until(deadline))
		if err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		if ok == nil || ok(m) {
			return m
		}
	}
	t.Fatalf("no matching %s", typ)
	return nil
}

func moveNo(m map[string]any) float64 {
	g, _ := m["game"].(map[string]any)
	n, _ := g["moveNumber"].(float64)
	return n
}

// pairAcross queues a on its node, then b on its node; b's node pairs and
// owns the game, a is black.
func pairAcross(t *testing.T, a, b *wsclient.Client) (ga, gb map[string]any) {
	t.Helper()
	send(t, a, map[string]any{"type": "matchmake"})
	if _, err := a.WaitType("matchmaking", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	send(t, b, map[string]any{"type": "matchmake"})
	ga = waitFrame(t, a, "game_start", nil)
	gb = waitFrame(t, b, "game_start", nil)
	if ga["youColor"] != "black" || gb["youColor"] != "white" {
		t.Fatalf("colors: %v %v", ga["youColor"], gb["youColor"])
	}
	return ga, gb
}

// play makes one move and waits until both clients have seen it.
func play(t *testing.T, mover, other *wsclient.Client, x, y int, n float64) {
	t.Helper()
	send(t, mover, map[string]any{"type": "move", "x": x, "y": y})
	for _, c := range []*wsclient.Client{mover, other} {
		waitFrame(t, c, "game_state", func(m map[string]any) bool { return moveNo(m) == n })
	}
}

func TestClusterCrossNodeGame(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *testCluster) {
		n0, n1 := c.add(), c.add()
		a, b := dial(t, n0.ts), dial(t, n1.ts)
		defer a.Close()
		defer b.Close()
		hello(t, a, "alice")
		hello(t, b, "bob")
		ga, gb := pairAcross(t, a, b)
		roomID := gameOf(t, ga)["roomId"].(string)

		errs := make(chan error, 2)
		go func() { _, err := playScriptedWin(a, "black", ga); errs <- err }()
		go func() { _, err := playScriptedWin(b, "white", gb); errs <- err }()
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		// game_over goes out before the owner settles; give it a moment.
		for deadline := time.Now().Add(2 * time.Second); n1.hub.Metrics().GamesPlayed != 1; {
			if time.Now().After(deadline) {
				t.Fatal("the pairing node must own (and finish) the game")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if n0.hub.Metrics().GamesPlayed != 0 {
			t.Fatal("only the owner finishes the game")
		}
		// The record is readable from the other node.
		deadline := time.Now().Add(2 * time.Second)
		for {
			resp, err := http.Get(n0.ts.URL + "/api/games/" + roomID)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("record not served by the other node: %d", resp.StatusCode)
			}
			time.Sleep(20 * time.Millisecond)
		}
		// Both are back in the lobby: each can queue again.
		send(t, a, map[string]any{"type": "matchmake"})
		if m, err := a.WaitType("matchmaking", 3*time.Second); err != nil || m["status"] != "queued" {
			t.Fatalf("alice not back in the lobby: %v %v", m, err)
		}
	})
}

func TestClusterLobbyJoinAndSpectate(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *testCluster) {
		n0, n1 := c.add(), c.add()
		a, b, v := dial(t, n0.ts), dial(t, n1.ts), dial(t, n0.ts)
		defer a.Close()
		defer b.Close()
		defer v.Close()
		hello(t, a, "alice")
		hello(t, b, "bob")
		hello(t, v, "viewer")

		send(t, a, map[string]any{"type": "create_room", "name": "跨节点"})
		created := waitFrame(t, a, "room", nil)
		roomID := created["room"].(map[string]any)["id"].(string)
		// The other node's lobby hears about it.
		waitFrame(t, b, "room_list", func(m map[string]any) bool {
			rooms, _ := m["rooms"].([]any)
			return len(rooms) == 1
		})
		send(t, b, map[string]any{"type": "join_room", "roomId": roomID})
		for _, cl := range []*wsclient.Client{a, b} {
			waitFrame(t, cl, "room", func(m map[string]any) bool { return m["action"] == "joined" })
		}
		ga := waitFrame(t, a, "game_start", nil)
		waitFrame(t, b, "game_start", nil)
		if ga["youColor"] != "black" {
			t.Fatalf("creator plays black: %v", ga["youColor"])
		}

		// A viewer on node0 watches the game node1 owns.
		send(t, v, map[string]any{"type": "spectate", "roomId": roomID})
		waitFrame(t, v, "spectate_start", nil)
		play(t, a, b, 7, 7, 1)
		waitFrame(t, v, "game_state", func(m map[string]any) bool { return moveNo(m) == 1 })

		send(t, a, map[string]any{"type": "resign"})
		for _, cl := range []*wsclient.Client{a, b, v} {
			over := waitFrame(t, cl, "game_over", nil)
			if over["reason"] != "resign" || over["winner"] != "white" {
				t.Fatalf("game_over: %v", over)
			}
		}
	})
}

// A player moves to another node mid-game (reconnect there, or open a
// second tab there while the first is still connected): the game goes on.
func TestClusterResumeAndTakeoverAcrossNodes(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *testCluster) {
		n0, n1 := c.add(), c.add()
		a, b := dial(t, n0.ts), dial(t, n1.ts)
		defer b.Close()
		wa := hello(t, a, "alice")
		hello(t, b, "bob")
		pairAcross(t, a, b)
		play(t, a, b, 7, 7, 1)

		// Reconnect on the other node.
		a.Close()
		waitFrame(t, b, "opponent_presence", func(m map[string]any) bool { return m["connected"] == false })
		a2 := dial(t, n1.ts)
		w2, err := a2.Hello("alice", wa["token"].(string))
		if err != nil || w2["resumed"] != true || w2["playerId"] != wa["playerId"] {
			t.Fatalf("resume on node1: %v %v", w2, err)
		}
		if w2["roomId"] == nil {
			t.Fatalf("in-game resume must name its room: %v", w2)
		}
		st := waitFrame(t, a2, "game_state", nil)
		if moveNo(st) != 1 {
			t.Fatalf("resumed game_state at move %v", moveNo(st))
		}
		waitFrame(t, b, "opponent_presence", func(m map[string]any) bool { return m["connected"] == true })
		play(t, b, a2, 8, 8, 2)

		// Takeover from node0 while still connected on node1.
		a3 := dial(t, n0.ts)
		defer a3.Close()
		if w3, err := a3.Hello("alice", wa["token"].(string)); err != nil || w3["resumed"] != true {
			t.Fatalf("takeover hello: %v %v", w3, err)
		}
		waitFrame(t, a3, "game_state", nil)
		// a2 may still drain a frame or two before its close lands.
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := a2.Read(time.Until(deadline)); err != nil {
				break // the old socket was closed by its node
			}
			if time.Now().After(deadline) {
				t.Fatal("old socket on node1 was not kicked")
			}
		}
		play(t, a3, b, 9, 9, 3)
		for _, m := range b.Collect(200 * time.Millisecond) {
			if mType(m) == "opponent_presence" && m["connected"] == false {
				t.Fatalf("a takeover must not look like a disconnect: %v", m)
			}
		}
	})
}

// The owner crashes mid-game: another node adopts the game from its
// snapshot once the lease lapses; the survivor's socket is re-attached by
// resync, the crashed node's player reconnects elsewhere, play resumes
// with every move intact and finishes with a full record.
func TestClusterFailover(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *testCluster) {
		n0, n1 := c.add(), c.add()
		a, b := dial(t, n0.ts), dial(t, n1.ts)
		defer a.Close()
		hello(t, a, "alice")
		wb := hello(t, b, "bob")
		ga, _ := pairAcross(t, a, b)
		roomID := gameOf(t, ga)["roomId"].(string)
		play(t, a, b, 7, 7, 1)
		play(t, b, a, 0, 0, 2)
		play(t, a, b, 8, 7, 3)
		time.Sleep(100 * time.Millisecond) // let the snapshot writer catch up

		n1.kill() // owner and bob's socket gone
		start := time.Now()
		c.expire(c.cfg.LeaseTTL + 50*time.Millisecond)
		st := waitFrame(t, a, "game_state", func(m map[string]any) bool { return gameOf(t, m)["roomId"] == roomID })
		if moveNo(st) != 3 || len(gameOf(t, st)["moves"].([]any)) != 3 {
			t.Fatalf("restored game lost moves: %v", gameOf(t, st)["moves"])
		}
		t.Logf("failover took %v", time.Since(start))
		if n0.hub.Metrics().OwnedGames != 1 {
			t.Fatal("node0 should own the restored game")
		}

		b2 := dial(t, n0.ts)
		defer b2.Close()
		if w, err := b2.Hello("bob", wb["token"].(string)); err != nil || w["resumed"] != true {
			t.Fatalf("bob resume: %v %v", w, err)
		}
		waitFrame(t, b2, "game_state", func(m map[string]any) bool { return moveNo(m) == 3 })

		for i, mv := range [][2]int{{0, 1}, {9, 7}, {0, 2}, {10, 7}, {0, 3}} {
			mover, other := b2, a
			if i%2 == 1 {
				mover, other = a, b2
			}
			send(t, mover, map[string]any{"type": "move", "x": mv[0], "y": mv[1]})
			for _, cl := range []*wsclient.Client{mover, other} {
				waitFrame(t, cl, "game_state", func(m map[string]any) bool { return moveNo(m) == float64(4+i) })
			}
		}
		send(t, a, map[string]any{"type": "move", "x": 11, "y": 7})
		over := waitFrame(t, a, "game_over", nil)
		if over["winner"] != "black" || over["reason"] != "five" {
			t.Fatalf("game_over: %v", over)
		}
		var rec protocol.GameRecord
		deadline := time.Now().Add(2 * time.Second)
		for {
			resp, err := http.Get(n0.ts.URL + "/api/games/" + roomID)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode == http.StatusOK {
				err = json.NewDecoder(resp.Body).Decode(&rec)
				_ = resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			_ = resp.Body.Close()
			if time.Now().After(deadline) {
				t.Fatal("no record after failover")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(rec.Moves) != 9 || rec.Moves[0] != (protocol.Pos{X: 7, Y: 7}) {
			t.Fatalf("record moves: %v", rec.Moves)
		}
	})
}

// A graceful shutdown hands games off at once (no lease wait), and a
// server restarted on the same Redis — here simply a new node — picks
// them up, AI games included.
func TestClusterHandoffAndRestart(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *testCluster) {
		c.cfg.LeaseTTL = 5 * time.Second // a lease wait would blow the budget below
		n0 := c.add()
		a := dial(t, n0.ts)
		wa := hello(t, a, "alice")
		send(t, a, map[string]any{"type": "play_ai", "difficulty": "easy"})
		waitFrame(t, a, "game_start", nil)
		send(t, a, map[string]any{"type": "move", "x": 7, "y": 7})
		waitFrame(t, a, "game_state", func(m map[string]any) bool { return moveNo(m) == 2 }) // AI replied
		time.Sleep(100 * time.Millisecond)

		// Restart: hand off, drop sockets, stop; a fresh node takes over.
		n0.hub.Handoff()
		n0.ts.CloseClientConnections()
		n0.hub.Close()
		start := time.Now()
		n1 := c.add()
		a2 := dial(t, n1.ts)
		defer a2.Close()
		if w, err := a2.Hello("alice", wa["token"].(string)); err != nil || w["resumed"] != true {
			t.Fatalf("resume after restart: %v %v", w, err)
		}
		st := waitFrame(t, a2, "game_state", func(m map[string]any) bool { return moveNo(m) == 2 })
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("handoff restore took %v, should not wait for the lease", d)
		}
		g := gameOf(t, st)
		if g["players"].(map[string]any)["white"].(map[string]any)["isAI"] != true {
			t.Fatalf("restored AI seat: %v", g["players"])
		}
		// The AI is alive after the restore.
		send(t, a2, map[string]any{"type": "move", "x": 7, "y": 8})
		waitFrame(t, a2, "game_state", func(m map[string]any) bool { return moveNo(m) == 4 })
	})
}

// Matchmaking stays exact under a concurrent rush spread over three nodes:
// everyone ends up in exactly one game, never two, never none.
func TestClusterConcurrentMatchmaking(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *testCluster) {
		nodes := []*testNode{c.add(), c.add(), c.add()}
		const players = 18
		clients := make([]*wsclient.Client, players)
		ids := make([]string, players)
		for i := range clients {
			clients[i] = dial(t, nodes[i%3].ts)
			defer clients[i].Close()
			ids[i] = hello(t, clients[i], fmt.Sprintf("p%02d", i))["playerId"].(string)
		}
		var wg sync.WaitGroup
		for _, cl := range clients {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = cl.SendMap(map[string]any{"type": "matchmake"})
			}()
		}
		wg.Wait()
		rooms := map[string][]string{}
		for i, cl := range clients {
			gs, err := cl.WaitType("game_start", 5*time.Second)
			if err != nil {
				t.Fatalf("player %d never started: %v", i, err)
			}
			id := gameOf(t, gs)["roomId"].(string)
			rooms[id] = append(rooms[id], ids[i])
		}
		if len(rooms) != players/2 {
			t.Fatalf("%d games for %d players", len(rooms), players)
		}
		for id, ps := range rooms {
			if len(ps) != 2 {
				t.Fatalf("room %s has %v", id, ps)
			}
		}
		for _, cl := range clients {
			for _, m := range cl.Collect(150 * time.Millisecond) {
				if mType(m) == "game_start" {
					t.Fatal("a player was paired twice")
				}
			}
		}
	})
}
