// Loadtest drives real games against a running gomoku-battle server:
// hello -> create/join pairing (or play_ai) -> alternate real moves until
// game_over, then re-pair, until -duration. It measures end-to-end
// connect+hello time, move->game_state broadcast latency (P50/P95/P99 via
// reservoir sampling), send/accept counts, loaded-window throughput,
// client CPU/RSS and server /metrics samples. JSON to stdout and -out.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

// latency keeps a bounded reservoir (Vitter R) of samples — an unbounded
// slice trends into GBs on long runs (M3-R11).
type latency struct {
	mu   sync.Mutex
	s    []float64
	seen int64
	sum  float64
	max  float64
}

const reservoirCap = 200_000

func (l *latency) add(d time.Duration) {
	ms := float64(d) / 1e6
	l.mu.Lock()
	l.seen++
	l.sum += ms
	if ms > l.max {
		l.max = ms
	}
	if len(l.s) < reservoirCap {
		l.s = append(l.s, ms)
	} else if j := rand.Int63n(l.seen); j < reservoirCap {
		l.s[j] = ms
	}
	l.mu.Unlock()
}

func (l *latency) stats() map[string]float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.s) == 0 {
		return map[string]float64{"count": 0}
	}
	s := append([]float64(nil), l.s...)
	sort.Float64s(s)
	q := func(p float64) float64 { return s[int(p*float64(len(s)-1)+0.5)] }
	return map[string]float64{
		"p50": q(.5), "p95": q(.95), "p99": q(.99),
		"max": l.max, "avg": l.sum / float64(l.seen), "count": float64(l.seen),
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

// pollMetrics snapshots /metrics once a second. samplesMu guards the slice
// against the main goroutine's final read (M3-R5); HTTP gets a timeout and
// decode failures are counted, not swallowed.
func pollMetrics(addr string, stop <-chan struct{}, samples *[]serverSample, mu *sync.Mutex, errs *errorCounter) {
	host := addr
	if u, err := url.Parse(addr); err == nil {
		host = u.Host
	}
	client := &http.Client{Timeout: 2 * time.Second}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			resp, err := client.Get("http://" + host + "/metrics")
			if err != nil {
				errs.add("metrics_poll")
				continue
			}
			var s serverSample
			s.At = time.Now()
			err = json.NewDecoder(resp.Body).Decode(&s)
			resp.Body.Close()
			if err != nil {
				errs.add("metrics_decode")
				continue
			}
			mu.Lock()
			*samples = append(*samples, s)
			mu.Unlock()
		}
	}
}

// userHZ is the jiffies-per-second for /proc/self/stat ticks, measured at
// runtime rather than assumed (M3-R11).
var userHZ = func() float64 {
	if b, err := exec.Command("getconf", "CLK_TCK").Output(); err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil && v > 0 {
			return v
		}
	}
	return 100
}()

func cpuRSS() (cpuSec float64, rssKB int64) {
	if b, err := os.ReadFile("/proc/self/stat"); err == nil {
		f := strings.Fields(string(b))
		if len(f) > 23 {
			u, _ := strconv.ParseInt(f[13], 10, 64)
			s, _ := strconv.ParseInt(f[14], 10, 64)
			cpuSec = float64(u+s) / userHZ
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
	var smu sync.Mutex
	stop := make(chan struct{})
	go pollMetrics(*addr, stop, &samples, &smu, errs)

	cpu0, _ := cpuRSS()
	start := time.Now()
	deadline := start.Add(*dur)
	// A closed channel broadcasts to EVERY waiter — time.After delivers to
	// exactly one, which silently wedged 199 of 200 pair goroutines.
	timer := make(chan time.Time)
	time.AfterFunc(time.Until(deadline), func() { close(timer) })
	var dialed, hellos, started, finished, sent, accepted int64
	var setupMs []float64 // dial+hello end-to-end (M3-R12)
	var cmu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < *rooms; i++ {
		select {
		case <-timer:
			goto launched
		default:
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var a, b *wsclient.Client
			// connect_ms = dial + hello, i.e. a usable conn (M3-R12).
			mk := func() *wsclient.Client {
				t0 := time.Now()
				c, err := wsclient.DialOpts(*addr, nil, false) // M3-R1: real pongs
				if err != nil {
					errs.add("dial")
					return nil
				}
				atomic.AddInt64(&dialed, 1)
				_, err = c.Hello(fmt.Sprintf("lt-%d-%d", i, rand.Int63()), "")
				if err != nil {
					errs.add("hello")
					c.Close()
					return nil
				}
				atomic.AddInt64(&hellos, 1)
				cmu.Lock()
				setupMs = append(setupMs, float64(time.Since(t0))/1e6)
				cmu.Unlock()
				return c
			}
			a = mk()
			if a == nil {
				return
			}
			if *mode != "ai" {
				b = mk()
				if b == nil {
					a.Close()
					return
				}
			}
			defer func() {
				a.Close()
				if b != nil {
					b.Close()
				}
			}()

			// ONE forwarder per conn for the pair's whole life — handshake
			// and game reads share its channel, no competing readers (R2).
			fa := make(chan frame, 256)
			fb := make(chan frame, 256)
			done := make(chan struct{})
			defer close(done)
			go forwarder(a, fa, done, errs)
			if b != nil {
				go forwarder(b, fb, done, errs)
			}

			for {
				select {
				case <-timer:
					return
				default:
				}
				var roomID string
				send := func(c *wsclient.Client, m map[string]any) error {
					return c.SendMapTimeout(m, 5*time.Second)
				}
				if *mode == "ai" {
					if err := send(a, map[string]any{"type": "play_ai", "difficulty": "normal"}); err != nil {
						errs.add("play_ai")
						return
					}
					roomID = "ai"
				} else {
					if err := send(a, map[string]any{"type": "create_room", "name": "lt"}); err != nil {
						errs.add("create_room")
						return
					}
					for roomID == "" {
						f, ok := nextFrame(fa, timer)
						if !ok {
							return
						}
						if f.typ == "room" && f.action == "created" {
							roomID = f.roomID
						} else if f.typ == "error" {
							errs.add(f.code) // R6: real reason, not a blanket read_room_created
							if f.code == "already_in_room" {
								// Seat release (onOver) races the game_over
								// frame; back off and retry create (R4: the
								// attempt is counted, never hidden).
								select {
								case <-timer:
									return
								case <-time.After(20 * time.Millisecond):
								}
								if err := send(a, map[string]any{"type": "create_room", "name": "lt"}); err != nil {
									errs.add("create_room")
									return
								}
							}
						}
					}
					if roomID == "" {
						return
					}
					if err := send(b, map[string]any{"type": "join_room", "roomId": roomID}); err != nil {
						errs.add("join_room")
						return
					}
				}
				atomic.AddInt64(&started, 1)
				if pairRun(a, b, fa, fb, timer, *interval, lat, &sent, &accepted, errs) {
					atomic.AddInt64(&finished, 1)
				} else {
					// Left a live game without game_over (desync, dead conn
					// path short of teardown): the old room still holds both
					// seats — leave it before re-pairing, else every create
					// gets already_in_room until the turn timer kills it.
					_ = a.SendMapTimeout(map[string]any{"type": "leave_room"}, 5*time.Second)
					if b != nil {
						_ = b.SendMapTimeout(map[string]any{"type": "leave_room"}, 5*time.Second)
					}
				}
			}
		}(i)
		time.Sleep(*ramp)
	}
launched:
	wg.Wait()
	close(stop)
	elapsed := time.Since(start)
	cpu1, rss := cpuRSS()

	// Loaded window = first sample with connections>0 .. last with >0.
	smu.Lock()
	loadStart, loadEnd := start, start
	for _, s := range samples {
		if s.Connections > 0 {
			if loadStart.Equal(start) || s.At.Before(loadStart) {
				loadStart = s.At
			}
			loadEnd = s.At
		}
	}
	smu.Unlock()
	loadWin := loadEnd.Sub(loadStart).Seconds()
	if loadWin <= 0 {
		loadWin = elapsed.Seconds()
	}

	sort.Float64s(setupMs)
	cq := func(p float64) float64 {
		if len(setupMs) == 0 {
			return 0
		}
		return setupMs[int(p*float64(len(setupMs)-1)+0.5)]
	}

	res := map[string]any{
		"mode": *mode, "rooms": *rooms,
		"duration_s":      elapsed.Seconds(),
		"loaded_window_s": loadWin,
		"conns_dialed":    dialed, "hellos": hellos,
		"games_started": started, "games_finished": finished,
		"moves_sent": sent, "moves_accepted": accepted,
		"connect_hello_ms": map[string]float64{"p50": cq(.5), "p95": cq(.95), "p99": cq(.99)},
		"move_latency_ms":  lat.stats(),
		"moves_per_s":      float64(accepted) / loadWin,
		"errors":           errs.m, "client_cpu_s": cpu1 - cpu0, "client_rss_mb": rss / 1024,
		"server_samples": samples,
	}
	js, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(js))
	if *out != "" && *out != "-" {
		_ = os.WriteFile(*out, js, 0o644)
	}
}
