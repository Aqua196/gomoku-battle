// Command wsclient is a scriptable websocket client for manual poking,
// integration tests and the M3 load harness. It reads one command per line
// from stdin and prints every received frame as a JSON line prefixed RECV.
//
// Usage:
//
//	wsclient -addr localhost:8080 -nick alice [-token <resume-token>]
//
// stdin commands:
//
//	hello [nick]            send hello (uses -nick/-token defaults)
//	raw <json>              send a verbatim JSON frame
//	create_room [name...]   create a room
//	join_room <roomId>
//	leave_room
//	list_rooms
//	matchmake | matchmake_cancel
//	play_ai <difficulty>
//	resign
//	move <x> <y>
//	ping [ts]
//	expect <type>           block until a frame of that type arrives (prints OK/FAIL)
//	sleep <ms>
//	quit
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/wsclient"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "server host:port")
	nick := flag.String("nick", "player", "default nick for hello")
	token := flag.String("token", "", "resume token for hello")
	expectTimeout := flag.Duration("expect-timeout", 10*time.Second, "timeout for expect")
	flag.Parse()

	c, err := wsclient.Dial("ws://"+*addr+"/ws", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer c.Close()

	// One reader goroutine fans every inbound frame out to subscribers:
	// the RECV printer plus any pending expect.
	fan := newFanout()
	go func() {
		for {
			m, err := c.Read(24 * time.Hour)
			if err != nil {
				fan.publish(map[string]any{"type": "_closed", "error": err.Error()})
				return
			}
			fan.publish(m)
		}
	}()
	go func() {
		sub := fan.subscribe()
		for m := range sub {
			if m["type"] == "_closed" {
				fmt.Println("CLOSED", m["error"])
				return
			}
			fmt.Println("RECV", mustJSON(m))
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		cmd, args := fields[0], fields[1:]
		var send map[string]any
		switch cmd {
		case "hello":
			n := *nick
			if len(args) > 0 {
				n = args[0]
			}
			send = map[string]any{"type": "hello", "nick": n}
			if *token != "" {
				send["token"] = *token
			}
		case "raw":
			raw := strings.TrimPrefix(line, "raw ")
			var m map[string]any
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				fmt.Println("FAIL bad json:", err)
				continue
			}
			send = m
		case "create_room":
			send = map[string]any{"type": "create_room"}
			if len(args) > 0 {
				send["name"] = strings.Join(args, " ")
			}
		case "join_room":
			if len(args) < 1 {
				fmt.Println("FAIL join_room needs a roomId")
				continue
			}
			send = map[string]any{"type": "join_room", "roomId": args[0]}
		case "leave_room", "list_rooms", "matchmake", "matchmake_cancel", "resign":
			send = map[string]any{"type": cmd}
		case "play_ai":
			d := "normal"
			if len(args) > 0 {
				d = args[0]
			}
			send = map[string]any{"type": "play_ai", "difficulty": d}
		case "move":
			if len(args) < 2 {
				fmt.Println("FAIL move needs x y")
				continue
			}
			x, ex := strconv.Atoi(args[0])
			y, ey := strconv.Atoi(args[1])
			if ex != nil || ey != nil {
				fmt.Println("FAIL move x y must be ints")
				continue
			}
			send = map[string]any{"type": "move", "x": x, "y": y}
		case "ping":
			send = map[string]any{"type": "ping"}
			if len(args) > 0 {
				if v, err := strconv.ParseInt(args[0], 10, 64); err == nil {
					send["ts"] = v
				}
			}
		case "expect":
			if len(args) < 1 {
				fmt.Println("FAIL expect needs a type")
				continue
			}
			fmt.Println("EXPECT", args[0])
			if waitForType(fan, args[0], *expectTimeout) {
				fmt.Println("OK", args[0])
			} else {
				fmt.Println("FAIL", args[0])
			}
			continue
		case "sleep":
			if len(args) > 0 {
				if ms, err := strconv.Atoi(args[0]); err == nil {
					time.Sleep(time.Duration(ms) * time.Millisecond)
				}
			}
			continue
		case "quit", "exit":
			return
		default:
			fmt.Println("FAIL unknown command:", cmd)
			continue
		}
		if send != nil {
			if err := c.SendMap(send); err != nil {
				fmt.Println("FAIL send:", err)
			}
		}
	}
}

// fanout broadcasts inbound frames to every subscriber channel.
type fanout struct {
	mu   sync.Mutex
	subs []chan map[string]any
}

func newFanout() *fanout { return &fanout{} }

func (f *fanout) subscribe() chan map[string]any {
	ch := make(chan map[string]any, 256)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	return ch
}

func (f *fanout) unsubscribe(ch chan map[string]any) {
	f.mu.Lock()
	for i, s := range f.subs {
		if s == ch {
			f.subs = append(f.subs[:i], f.subs[i+1:]...)
			break
		}
	}
	f.mu.Unlock()
}

func (f *fanout) publish(m map[string]any) {
	f.mu.Lock()
	for _, s := range f.subs {
		select {
		case s <- m:
		default:
		}
	}
	f.mu.Unlock()
}

func waitForType(f *fanout, want string, timeout time.Duration) bool {
	sub := f.subscribe()
	defer f.unsubscribe(sub)
	deadline := time.After(timeout)
	for {
		select {
		case m := <-sub:
			if m["type"] == want {
				return true
			}
			if m["type"] == "_closed" {
				return false
			}
		case <-deadline:
			return false
		}
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<unmarshalable>"
	}
	return string(b)
}
