package ws

import (
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/Aqua196/gomoku-battle/backend/internal/hub"
	"github.com/Aqua196/gomoku-battle/backend/internal/protocol"
	"github.com/gorilla/websocket"
)

// Server upgrades HTTP requests into websocket sessions and routes every
// inbound frame to the hub. It also assembles the production HTTP mux
// (healthz, metrics, optional pprof, optional SPA static hosting).
type Server struct {
	hub      *hub.Hub
	cfg      protocol.Config
	upgrader websocket.Upgrader
}

// session is per-connection state owned by the read pump goroutine.
type session struct {
	srv    *Server
	c      *conn
	player *hub.Player // set once hello succeeds
}

// NewServer builds the gateway. allowAnyOrigin passes every Origin header
// (dev/vite-proxy only); the default policy requires Origin's host to equal
// the request host, while permitting absent Origin (non-browser clients).
func NewServer(h *hub.Hub, cfg protocol.Config, allowAnyOrigin bool) *Server {
	return &Server{
		hub: h,
		cfg: cfg,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				if allowAnyOrigin {
					return true
				}
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // CLI/server-side clients carry no Origin
				}
				u, err := url.Parse(origin)
				return err == nil && u.Host == r.Host
			},
		},
	}
}

// Handler assembles the full HTTP mux. staticDir may not exist (dev mode) in
// which case no static route is mounted; pprof mounts /debug/pprof/* only
// when enabled (PROTOCOL §7).
func (s *Server) Handler(staticDir string, enablePprof bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(protocol.Marshal(s.hub.Metrics()))
	})
	if enablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
		mux.Handle("/", spaHandler(staticDir))
	}
	return mux
}

// spaHandler serves staticDir with a fallback to index.html so the SPA owns
// client-side routes (D8).
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path) // force inside dir
		p := filepath.Join(dir, clean)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

// handleWS upgrades and runs the read pump on the handler goroutine.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // upgrade already wrote the error
	}
	c := newConn(wsConn, s.cfg)
	sess := &session{srv: s, c: c}
	s.hub.ConnOpened()
	go c.writePump()
	sess.readPump()
}

// send marshals and enqueues one frame to this connection.
func (sess *session) send(msg any) {
	data := protocol.Marshal(msg)
	if !sess.c.Send(data) {
		sess.c.Close()
	}
}

func (sess *session) sendErr(code, message string) {
	sess.send(protocol.NewError(code, message))
}

// readPump runs the frame loop until error or close. On entry the deadline
// is the hello window; pong extends it afterwards (PROTOCOL §1/§6).
func (sess *session) readPump() {
	c := sess.c
	defer func() {
		c.Close()
		sess.srv.hub.ConnClosed()
		if sess.player != nil {
			sess.srv.hub.PlayerDisconnected(sess.player, c)
		}
	}()

	c.ws.SetReadLimit(1 << 16)
	_ = c.ws.SetReadDeadline(time.Now().Add(sess.srv.cfg.HelloTimeout))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(sess.srv.cfg.WSPongWait))
	})

	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			if sess.player == nil && isTimeout(err) {
				sess.sendErr(protocol.EHelloTimeout, "hello not received in time")
			}
			return
		}
		if sess.player == nil {
			sess.handleHello(data)
			continue
		}
		sess.dispatch(data)
	}
}

// handleHello validates and binds the hello frame. On success it swaps the
// read deadline to the pong window and sends welcome; a resumed player in a
// live game then receives game_state via the actor.
func (sess *session) handleHello(data []byte) {
	in, err := protocol.ParseInbound(data)
	if err != nil {
		sess.sendErr(protocol.EBadRequest, "malformed hello")
		return
	}
	if in.Type != protocol.THello {
		if protocol.IsKnownClientType(in.Type) {
			sess.sendErr(protocol.EUnauthorized, "hello required before any other message")
		}
		return // unknown types are ignored per PROTOCOL §0
	}
	if n := utf8.RuneCountInString(in.Nick); n < 1 || n > 32 {
		sess.sendErr(protocol.EBadRequest, "nick must be 1-32 characters")
		return
	}
	p, resumed, code := sess.srv.hub.Hello(sess.c, in.Nick, in.Token)
	if code != "" {
		sess.sendErr(code, "invalid or busy session token")
		return
	}
	sess.player = p
	_ = sess.c.ws.SetReadDeadline(time.Now().Add(sess.srv.cfg.WSPongWait))
	sess.send(protocol.NewWelcome(p.ID(), p.Token(), p.Nick(), resumed))
	if resumed {
		// welcome first, then the live game view (PROTOCOL §1).
		sess.srv.hub.ResumeGame(p)
	}
}

// dispatch routes a post-hello frame. Unknown types are ignored and
// malformed frames answered with bad_request (PROTOCOL §0).
func (sess *session) dispatch(data []byte) {
	in, err := protocol.ParseInbound(data)
	if err != nil {
		sess.sendErr(protocol.EBadRequest, "malformed message")
		return
	}
	h := sess.srv.hub
	p := sess.player
	switch in.Type {
	case protocol.TPing:
		h.AppPing(p, in.TS)
	case protocol.TCreateRoom:
		h.CreateRoom(p, in.Name)
	case protocol.TJoinRoom:
		if in.RoomID == "" {
			sess.sendErr(protocol.EBadRequest, "roomId required")
			return
		}
		h.JoinRoom(p, in.RoomID)
	case protocol.TLeaveRoom:
		h.LeaveRoom(p)
	case protocol.TListRooms:
		h.ListRooms(p)
	case protocol.TMatchmake:
		h.Matchmake(p)
	case protocol.TMatchmakeCancel:
		h.MatchmakeCancel(p)
	case protocol.TPlayAI:
		h.PlayAI(p, in.Difficulty)
	case protocol.TResign:
		h.Resign(p)
	case protocol.TMove:
		if in.X == nil || in.Y == nil {
			sess.sendErr(protocol.EBadRequest, "move requires integer x and y")
			return
		}
		h.Move(p, *in.X, *in.Y)
	default:
		// Unknown type: ignore per PROTOCOL §0.
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
