// Package ws is the websocket gateway: upgrade, per-connection read/write
// pumps, hello binding and heartbeat (DESIGN §3: two goroutines per
// connection, buffered outbound channel, slow consumers are kicked).
package ws

import (
	"sync"
	"time"

	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/gorilla/websocket"
)

// conn wraps one websocket with the buffered outbound channel the hub
// writes through. It implements hub.Conn.
type conn struct {
	ws   *websocket.Conn
	send chan []byte
	quit chan struct{} // closed by Close; writePump flushes then exits
	cfg  protocol.Config
	once sync.Once
}

func newConn(wsConn *websocket.Conn, cfg protocol.Config) *conn {
	return &conn{
		ws:   wsConn,
		send: make(chan []byte, cfg.SendBuf),
		quit: make(chan struct{}),
		cfg:  cfg,
	}
}

// Send enqueues a frame without blocking; a full buffer marks the client a
// slow consumer (PROTOCOL §6 SEND_BUF), reported to the caller who closes.
func (c *conn) Send(data []byte) bool {
	select {
	case c.send <- data:
		return true
	case <-c.quit:
		return false
	default:
		return false
	}
}

// Close asks the write pump to flush remaining frames, emit a close frame
// and terminate the socket. Idempotent.
func (c *conn) Close() {
	c.once.Do(func() { close(c.quit) })
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
	for {
		select {
		case data := <-c.send:
			if !write(data) {
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
				case data := <-c.send:
					if !write(data) {
						return
					}
					continue
				default:
				}
				break
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait))
			_ = c.ws.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}
