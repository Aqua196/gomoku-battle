// Loadtest drives real games against a running gomoku-battle server:
// hello -> matchmake (or play_ai) -> alternate real moves until
// game_over or -duration. It measures move->game_state broadcast latency
// (P50/P95/P99), error counts, throughput, client CPU/RSS and server
// /metrics samples, and writes JSON + a text table to stdout and -out.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

type pair struct {
	id  int
	a   *wsclient.Client
	b   *wsclient.Client
	aid string
	bid string
}

// latency is a quantile recorder.
type latency struct {
	mu  sync.Mutex
	s   []float64
	cnt int64
	sum float64
	max float64
}

func (l *latency) add(d time.Duration) {
	ms := float64(d) / 1e6
	l.mu.Lock()
	l.s = append(l.s, ms)
	l.sum += ms
	if ms > l.max {
		l.max = ms
	}
	l.mu.Unlock()
	atomic.AddInt64(&l.cnt, 1)
}

func (l *latency) quantiles() map[string]float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.s) == 0 {
		return map[string]float64{}
	}
	s := append([]float64(nil), l.s...)
	sort.Float64s(s)
	q := func(p float64) float64 {
		i := int(p*float64(len(s)-1) + 0.5)
		return s[i]
	}
	return map[string]float64{
		"p50": q(0.50), "p95": q(0.95), "p99": q(0.99),
		"max": s[len(s)-1], "avg": l.sum / float64(len(s)),
	}
}

type errorCounter struct {
	mu sync.Mutex
	m  map[string]int
}

func (e *errorCounter) add(k string) {
	e.mu.Lock()
	if e.m == nil {
		e.m = map[string]int{}
	}
	e.m[k]++
	e.mu.Unlock()
}

type serverSample struct {
	At          time.Time `json:"at"`
	Connections int       `json:"connections"`
	Lobby       int       `json:"lobbyPlayers"`
	Rooms       int       `json:"rooms"`
	Active      int       `json:"activeGames"`
	Played      int       `json:"gamesPlayed"`
}

func pollMetrics(addr string, stop <-chan struct{}, out *[]serverSample) {
	host := addr
	if u, err := url.Parse(addr); err == nil {
		host = u.Host
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			resp, err := http.Get("http://" + host + "/metrics")
			if err != nil {
				continue
			}
			var s serverSample
			s.At = time.Now()
			_ = json.NewDecoder(resp.Body).Decode(&s)
			resp.Body.Close()
			*out = append(*out, s)
		}
	}
}

func cpuRSS() (cpuSec float64, rssKB int64) {
	if b, err := os.ReadFile("/proc/self/stat"); err == nil {
		f := strings.Fields(string(b))
		if len(f) > 23 {
			u, _ := strconv.ParseInt(f[13], 10, 64)
			s, _ := strconv.ParseInt(f[14], 10, 64)
			cpuSec = float64(u+s) / 100.0
		}
	}
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(ln, "VmRSS:") {
				fmt.Sscanf(strings.TrimPrefix(ln, "VmRSS:"), "%d", &rssKB)
			}
		}
	}
	return
}

func main() {
	addr := flag.String("addr", "ws://localhost:8080/ws", "server ws endpoint")
	rooms := flag.Int("rooms", 100, "concurrent games (pvp: 2 conns each; ai: 1)")
	dur := flag.Duration("duration", 60*time.Second, "test duration")
	interval := flag.Duration("move-interval", 50*time.Millisecond, "base think between moves (jittered)")
	ramp := flag.Duration("ramp", 5*time.Millisecond, "delay between starting pairs")
	mode := flag.String("mode", "pvp", "pvp|ai")
	out := flag.String("out", "", "result file (JSON); - for stdout only")
	flag.Parse()

	lat := &latency{}
	errs := &errorCounter{}
	var samples []serverSample
	stop := make(chan struct{})
	go pollMetrics(*addr, stop, &samples)

	cpu0, _ := cpuRSS()
	start := time.Now()
	deadline := start.Add(*dur)
	var dialed, hellos, started, finished int64
	var connectMs []float64
	var cmu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < *rooms; i++ {
		if time.Now().After(deadline) {
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := &pair{id: i}
			slots := []**wsclient.Client{&p.a, &p.b}
			for _, slot := range slots {
				if *mode == "ai" && slot == &p.b {
					continue
				}
				t0 := time.Now()
				c, err := wsclient.DialOpts(*addr, nil, true)
				if err != nil {
					errs.add("dial")
					return
				}
				cmu.Lock()
				connectMs = append(connectMs, float64(time.Since(t0))/1e6)
				cmu.Unlock()
				atomic.AddInt64(&dialed, 1)
				w, err := c.Hello(fmt.Sprintf("lt-%d-%d", i, rand.Int63()), "")
				if err != nil {
					errs.add("hello")
					c.Close()
					return
				}
				atomic.AddInt64(&hellos, 1)
				*slot = c
				if slot == &p.a {
					p.aid, _ = w["playerId"].(string)
				} else {
					p.bid, _ = w["playerId"].(string)
				}
			}
			// Sustained load: after each game_over, re-pair into a fresh
			// room until the shared deadline so pressure stays constant.
			for time.Now().Before(deadline) {
				if *mode == "ai" {
					if err := p.a.SendMap(map[string]any{"type": "play_ai", "difficulty": "normal"}); err != nil {
						errs.add("play_ai")
						return
					}
				} else {
					// Deterministic pairing: a creates a room, reads the
					// room{created} id back, b joins it — no FIFO crosstalk.
					if err := p.a.SendMap(map[string]any{"type": "create_room", "name": "lt"}); err != nil {
						errs.add("create_room")
						return
					}
					var roomID string
					for roomID == "" && time.Now().Before(deadline) {
						m, err := p.a.Read(5 * time.Second)
						if err != nil {
							errs.add("read_room_created")
							return
						}
						if m["type"] == "room" && m["action"] == "created" {
							if r, ok := m["room"].(map[string]any); ok {
								roomID, _ = r["id"].(string)
							}
						}
					}
					if roomID == "" {
						errs.add("room_id")
						return
					}
					if err := p.b.SendMap(map[string]any{"type": "join_room", "roomId": roomID}); err != nil {
						errs.add("join_room")
						return
					}
				}
				atomic.AddInt64(&started, 1)
				pairRun(p.a, p.b, deadline, *interval, lat, errs)
				atomic.AddInt64(&finished, 1)
			}
			if p.a != nil {
				p.a.Close()
			}
			if p.b != nil {
				p.b.Close()
			}
		}(i)
		time.Sleep(*ramp)
	}
	wg.Wait()
	close(stop)
	elapsed := time.Since(start)
	cpu1, rss := cpuRSS()

	sort.Float64s(connectMs)
	cq := func(p float64) float64 {
		if len(connectMs) == 0 {
			return 0
		}
		return connectMs[int(p*float64(len(connectMs)-1)+0.5)]
	}

	res := map[string]any{
		"mode": *mode, "rooms": *rooms, "duration_s": elapsed.Seconds(),
		"conns_dialed": dialed, "hellos": hellos, "games_started": started, "games_finished": finished,
		"connect_ms":      map[string]float64{"p50": cq(.5), "p95": cq(.95), "p99": cq(.99)},
		"move_latency_ms": lat.quantiles(), "moves_total": atomic.LoadInt64(&lat.cnt),
		"moves_per_s": float64(atomic.LoadInt64(&lat.cnt)) / elapsed.Seconds(),
		"errors":      errs.m, "client_cpu_s": cpu1 - cpu0, "client_rss_mb": rss / 1024,
		"server_samples": samples,
	}
	js, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(js))
	if *out != "" && *out != "-" {
		_ = os.WriteFile(*out, js, 0o644)
	}
}
