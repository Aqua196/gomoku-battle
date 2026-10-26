// Package wsclient is a programmable websocket client shared by the e2e
// tests and cmd/wsclient (reused for M3 load generation).
//
// A pump goroutine reads the socket without deadlines and feeds a channel,
// so a client-side read timeout never poisons the connection the way a
// gorilla read-deadline timeout does (a timed-out read leaves gorilla's
// parser mid-frame and latches the error onto all later reads).
package wsclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const defaultWriteWait = 10 * time.Second

type readResult struct {
	m   map[string]any
	err error
}

// Client is a single websocket connection with a buffered inbound queue.
type Client struct {
	conn     *websocket.Conn
	incoming chan readResult
	wmu      sync.Mutex
	mu       sync.Mutex
	pending  []map[string]any
}

// Dial connects to a ws:// or http(s):// URL. Extra headers are optional.
func Dial(rawurl string, header http.Header) (*Client, error) {
	return DialOpts(rawurl, header, false)
}

// DialOpts connects like Dial and optionally suppresses pong replies to
// server pings (gorilla requires handlers be installed before the first
// read, so this is a dial-time option, not a later mutation).
func DialOpts(rawurl string, header http.Header, suppressPong bool) (*Client, error) {
	u := rawurl
	u = strings.Replace(u, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	conn, resp, err := websocket.DefaultDialer.Dial(parsed.String(), header)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	if suppressPong {
		conn.SetPingHandler(func(string) error { return nil })
	} else {
		// Override gorilla's 1s defaultPingHandler writeWait so a busy test
		// runner under -race never leaves a 1s write deadline on net.Conn.
		conn.SetPingHandler(func(message string) error {
			err := conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(defaultWriteWait))
			var ne net.Error
			if errors.Is(err, websocket.ErrCloseSent) || (errors.As(err, &ne) && ne.Timeout()) {
				return nil
			}
			return err
		})
	}
	c := &Client{conn: conn, incoming: make(chan readResult, 256)}
	go c.pump()
	return c, nil
}

// pump owns the socket read path: exactly one reader, no deadlines.
// Deliveries are non-blocking: a stalled consumer would freeze the pump
// and hide the disconnect notice, so a full inbox closes the conn instead
// (the reader then sees a concrete error, not a silent wedge).
func (c *Client) pump() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.deliver(readResult{err: err})
			return
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			c.deliver(readResult{err: fmt.Errorf("wsclient: non-JSON frame: %w", err)})
			continue
		}
		c.deliver(readResult{m: m})
	}
}

func (c *Client) deliver(r readResult) {
	select {
	case c.incoming <- r:
	default:
		_ = c.conn.Close() // consumer stalled: force an observable error
	}
}

// Send writes a JSON object. A mutex serializes concurrent writers:
// gorilla permits one writer at a time and interleaved frames corrupt.
// Always refresh the write deadline so a prior WriteControl (pong) deadline
// never leaks onto subsequent data frames.
func (c *Client) Send(v any) error {
	return c.SendTimeout(v, defaultWriteWait)
}

// SendMap is Send for a map literal.
func (c *Client) SendMap(m map[string]any) error { return c.Send(m) }

// SendTimeout is Send with a bounded write window: if the peer stops
// draining, the write fails instead of blocking forever. A timed-out
// write leaves the conn write-broken, so callers should treat an error
// come-from-deadline as fatal for that conn.
func (c *Client) SendTimeout(v any, d time.Duration) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(d))
	err = c.conn.WriteMessage(websocket.TextMessage, data)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

// SendMapTimeout is SendTimeout for a map literal.
func (c *Client) SendMapTimeout(m map[string]any, d time.Duration) error {
	return c.SendTimeout(m, d)
}

// SendRaw writes raw text (for malformed-frame tests).
func (c *Client) SendRaw(s string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(defaultWriteWait))
	err := c.conn.WriteMessage(websocket.TextMessage, []byte(s))
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

// Hello performs the hello handshake and returns the welcome frame.
func (c *Client) Hello(nick, token string) (map[string]any, error) {
	msg := map[string]any{"type": "hello", "nick": nick}
	if token != "" {
		msg["token"] = token
	}
	if err := c.SendTimeout(msg, 5*time.Second); err != nil {
		return nil, err
	}
	return c.WaitType("welcome", 5*time.Second)
}

// Read returns the next received frame. Buffered frames from WaitType
// peeking are delivered first.
func (c *Client) Read(timeout time.Duration) (map[string]any, error) {
	c.mu.Lock()
	if len(c.pending) > 0 {
		m := c.pending[0]
		c.pending = c.pending[1:]
		c.mu.Unlock()
		return m, nil
	}
	c.mu.Unlock()
	return c.readIncoming(timeout)
}

// readIncoming waits for the next pumped frame, ignoring pending.
func (c *Client) readIncoming(timeout time.Duration) (map[string]any, error) {
	select {
	case rr := <-c.incoming:
		return rr.m, rr.err
	case <-time.After(timeout):
		return nil, errors.New("wsclient: read timeout")
	}
}

// WaitType reads frames until one of the wanted type arrives. Already
// buffered (skipped) frames are scanned first; new non-matching frames are
// buffered for later Read calls. Returns the matched frame.
func (c *Client) WaitType(want string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		found := -1
		for i, m := range c.pending {
			if m["type"] == want {
				found = i
				break
			}
		}
		if found >= 0 {
			m := c.pending[found]
			c.pending = append(c.pending[:found], c.pending[found+1:]...)
			c.mu.Unlock()
			return m, nil
		}
		c.mu.Unlock()

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, errors.New("wsclient: timeout waiting for " + want)
		}
		m, err := c.readIncoming(remaining)
		if err != nil {
			return nil, err
		}
		if m["type"] == want {
			return m, nil
		}
		c.mu.Lock()
		c.pending = append(c.pending, m)
		c.mu.Unlock()
	}
}

// Collect drains frames for up to timeout and returns them in order.
func (c *Client) Collect(timeout time.Duration) []map[string]any {
	var out []map[string]any
	deadline := time.Now().Add(timeout)
	for {
		m, err := c.Read(time.Until(deadline))
		if err != nil {
			return out
		}
		out = append(out, m)
	}
}

// Close terminates the socket.
func (c *Client) Close() { _ = c.conn.Close() }
