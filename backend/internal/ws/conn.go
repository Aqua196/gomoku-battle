// Package ws is the websocket gateway: upgrade, per-connection read/write
// pumps, hello binding and heartbeat (DESIGN §3: two goroutines per
// connection, buffered outbound channel, slow consumers are kicked).
package ws

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/gorilla/websocket"
)

// outMsg is one queued outbound frame. roomList frames carry a marker so the
// write pump can resolve them against the latest snapshot instead.
type outMsg struct {
	data     []byte
	roomList bool
}

// conn wraps one websocket with the buffered outbound channel the hub
// writes through. It implements hub.Conn.
type conn struct {
	ws   *websocket.Conn
	send chan outMsg
	quit chan struct{} // closed by Close; writePump flushes then exits
	cfg  protocol.Config
	once sync.Once
	// goingAway picks close code 1001 over 1000: the server is shutting
	// down and the client should reconnect rather than treat it as final.
	goingAway atomic.Bool

	// room_list coalescing (PROTOCOL §2 merge requirement): at most one
	// marker occupies the send buffer; it always resolves to rlPending,
	// the newest snapshot, so a churn burst can never backlog 64 stale
	// copies and trip the slow-consumer kick on a healthy client.
	rlMu      sync.Mutex
	rlPending []byte
	rlQueued  bool
}

func newConn(wsConn *websocket.Conn, cfg protocol.Config) *conn {
	return &conn{
		ws:   wsConn,
		send: make(chan outMsg, cfg.SendBuf),
		quit: make(chan struct{}),
		cfg:  cfg,
	}
}

// Send enqueues a frame without blocking; a full buffer marks the client a
// slow consumer (PROTOCOL §6 SEND_BUF), reported to the caller who closes.
func (c *conn) Send(data []byte) bool {
	select {
	case <-c.quit:
		return false
	default:
	}
	select {
	case c.send <- outMsg{data: data}:
		return true
	case <-c.quit:
		return false
	default:
		return false
	}
}

// SendRoomList queues a room_list snapshot with dirty-bit merge semantics:
// if an unwritten room_list marker is already queued, only the snapshot is
// replaced — the buffer never holds more than one pending room_list.
func (c *conn) SendRoomList(data []byte) bool {
	c.rlMu.Lock()
	c.rlPending = data
	if c.rlQueued {
		c.rlMu.Unlock()
		return true // the pending marker will emit this newest snapshot
	}
	c.rlQueued = true
	c.rlMu.Unlock()

	select {
	case <-c.quit:
		return false
	default:
	}
	select {
	case c.send <- outMsg{roomList: true}:
		return true
	case <-c.quit:
		return false
	default:
		// No slot even for the marker: the connection is genuinely backed
		// up with other traffic — report slow consumer.
		c.rlMu.Lock()
		c.rlQueued = false
		c.rlPending = nil
		c.rlMu.Unlock()
		return false
	}
}

// Close asks the write pump to flush remaining frames, emit a close frame
// and terminate the socket. Idempotent.
func (c *conn) Close() {
	c.once.Do(func() { close(c.quit) })
}

// GoAway closes with 1001 "going away" (server shutdown).
func (c *conn) GoAway() {
	c.goingAway.Store(true)
	c.Close()
}

// takeRoomList swaps out the pending snapshot for a queued marker.
func (c *conn) takeRoomList() []byte {
	c.rlMu.Lock()
	defer c.rlMu.Unlock()
	data := c.rlPending
	c.rlPending = nil
	c.rlQueued = false
	return data
}

// writePump drains the send channel and emits the ws-level heartbeat pings.
// On quit it drains whatever is still buffered so terminal frames (e.g. the
// hello_timeout error) reach the wire before the close.
func (c *conn) writePump() {
	ticker := time.NewTicker(c.cfg.WSPingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.ws.Close()
	}()
	write := func(data []byte) bool {
		_ = c.ws.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait))
		return c.ws.WriteMessage(websocket.TextMessage, data) == nil
	}
	writeMsg := func(m outMsg) bool {
		if m.roomList {
			data := c.takeRoomList()
			if data == nil {
				return true // snapshot already consumed; nothing to write
			}
			return write(data)
		}
		return write(m.data)
	}
	for {
		select {
		case m := <-c.send:
			if !writeMsg(m) {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.quit:
			for {
				select {
				case m := <-c.send:
					if !writeMsg(m) {
						return
					}
					continue
				default:
				}
				break
			}
			code := websocket.CloseNormalClosure
			if c.goingAway.Load() {
				code = websocket.CloseGoingAway
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait))
			_ = c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""))
			return
		}
	}
}
