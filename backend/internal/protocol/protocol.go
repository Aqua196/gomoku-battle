// Package protocol defines the frozen wire contract of PROTOCOL.md v1 (M1):
// message type names, error codes, payload shapes and the tunable time
// constants. Everything here is shared by the hub, game actors and the ws
// gateway so the contract has a single source of truth.
package protocol

import (
	"encoding/json"
	"time"
)

// Board geometry and cell values (PROTOCOL §0).
const (
	BoardSize = 15

	CellEmpty = 0
	CellBlack = 1
	CellWhite = 2

	ColorBlack = "black"
	ColorWhite = "white"
)

// Client -> server message types.
const (
	THello           = "hello"
	TPing            = "ping"
	TCreateRoom      = "create_room"
	TJoinRoom        = "join_room"
	TLeaveRoom       = "leave_room"
	TListRooms       = "list_rooms"
	TMatchmake       = "matchmake"
	TMatchmakeCancel = "matchmake_cancel"
	TPlayAI          = "play_ai"
	TResign          = "resign"
	TMove            = "move"
)

// Server -> client message types.
const (
	TWelcome          = "welcome"
	TPong             = "pong"
	TError            = "error"
	TRoom             = "room"
	TRoomList         = "room_list"
	TMatchmaking      = "matchmaking"
	TGameStart        = "game_start"
	TGameState        = "game_state"
	TGameOver         = "game_over"
	TOpponentPresence = "opponent_presence"
)

// Error codes (PROTOCOL §8).
const (
	EBadRequest   = "bad_request"
	EHelloTimeout = "hello_timeout"
	// EUnauthorized survives only for pre-hello rejection; hello token
	// takeover (PROTOCOL §1) no longer emits it.
	EUnauthorized      = "unauthorized"
	ENotInLobby        = "not_in_lobby"
	EAlreadyInRoom     = "already_in_room"
	ENotInRoom         = "not_in_room"
	ERoomNotFound      = "room_not_found"
	ERoomFull          = "room_full"
	ENotYourTurn       = "not_your_turn"
	EIllegalMove       = "illegal_move"
	EGameOver          = "game_over"
	EInvalidDifficulty = "invalid_difficulty"
	EInvalidNick       = "invalid_nick"
	ENotImplemented    = "not_implemented"
	ERateLimited       = "rate_limited"
	EInternal          = "internal"
)

// Room / game / matchmaking status and reason literals.
const (
	RoomWaiting  = "waiting"
	RoomPlaying  = "playing"
	RoomFinished = "finished"

	RoomCreated = "created"
	RoomJoined  = "joined"
	RoomLeft    = "left"

	MatchQueued    = "queued"
	MatchCancelled = "cancelled"

	ReasonFive              = "five"
	ReasonDraw              = "draw"
	ReasonTimeout           = "timeout"
	ReasonResign            = "resign"
	ReasonDisconnectTimeout = "disconnect_timeout"
	// ReasonAbandoned retires a game both sides abandoned past AbandonTTL
	// (PROTOCOL §3: winner is null, like draw).
	ReasonAbandoned = "abandoned"
)

// Config carries the PROTOCOL §6 time constants. They are injected so tests
// can shrink windows instead of sleeping on real 30s timers.
type Config struct {
	HelloTimeout    time.Duration // deadline for the hello frame after connect
	WSPingPeriod    time.Duration // ws-level ping interval
	WSPongWait      time.Duration // read deadline extended by each pong
	WriteWait       time.Duration // per-write deadline
	MoveTimeout     time.Duration // per-turn move deadline
	ReconnectWindow time.Duration // disconnect-forfeit window
	SendBuf         int           // per-connection outbound buffer (msgs)
	// SessionTTL expires disconnected, non-playing sessions (REVIEW R4).
	// Playing sessions are exempt: their lifecycle belongs to the game.
	SessionTTL time.Duration
	// SweepInterval is how often the hub scans for expired sessions.
	SweepInterval time.Duration
	// AbandonTTL cleans up a game in which BOTH sides stayed offline past
	// their reconnect windows (PROTOCOL §5: verdict suspended until a
	// reconnect; abandoned games die on this TTL).
	AbandonTTL time.Duration
	// AIThink caps the AI driver's per-move budget; zero means each
	// difficulty preset's default (PROTOCOL §8: <= 2s per move).
	AIThink time.Duration
	// RoomListFlush coalesces lobby room_list broadcasts: mutations mark
	// the list dirty and one flush interval emits a single render+push.
	// Zero means every mutation broadcasts immediately (tests).
	RoomListFlush time.Duration

	// MsgRate and MsgBurst bound inbound frames per connection (token
	// bucket: MsgRate refills per second up to MsgBurst). Frames over the
	// limit are dropped with error{code:"rate_limited"}. Zero disables.
	MsgRate  float64
	MsgBurst int
	// MaxConnsPerIP caps concurrent websockets from one client address;
	// the upgrade beyond it answers 429. Zero disables.
	MaxConnsPerIP int
	// TrustProxy takes the client address from the first X-Forwarded-For
	// hop instead of the socket peer (only behind a trusted reverse proxy).
	TrustProxy bool
}

// DefaultConfig returns the production constants from PROTOCOL §6.
func DefaultConfig() Config {
	return Config{
		HelloTimeout:    5 * time.Second,
		WSPingPeriod:    10 * time.Second,
		WSPongWait:      30 * time.Second,
		WriteWait:       10 * time.Second,
		MoveTimeout:     30 * time.Second,
		ReconnectWindow: 30 * time.Second,
		SendBuf:         64,
		SessionTTL:      5 * time.Minute,
		SweepInterval:   60 * time.Second,
		AbandonTTL:      10 * time.Minute,
		AIThink:         0,
		RoomListFlush:   50 * time.Millisecond,
		MsgRate:         20,
		MsgBurst:        40,
		MaxConnsPerIP:   32,
	}
}

// NowMs is the Unix-millis timestamp used by every payload field.
func NowMs() int64 { return time.Now().UnixMilli() }

// IsKnownClientType reports whether t names a client -> server message type.
func IsKnownClientType(t string) bool {
	switch t {
	case THello, TPing, TCreateRoom, TJoinRoom, TLeaveRoom, TListRooms,
		TMatchmake, TMatchmakeCancel, TPlayAI, TResign, TMove:
		return true
	}
	return false
}

// Inbound is the union of every client -> server payload. Messages are first
// validated as JSON objects with a known shape; per-type field requirements
// are enforced by the dispatcher.
type Inbound struct {
	Type       string `json:"type"`
	Nick       string `json:"nick"`
	Token      string `json:"token"`
	Name       string `json:"name"`
	RoomID     string `json:"roomId"`
	Difficulty string `json:"difficulty"`
	X          *int   `json:"x"`
	Y          *int   `json:"y"`
	TS         *int64 `json:"ts"`
}

// ParseInbound decodes one client frame. It errors on non-JSON input, on a
// missing/empty type and on typed fields that do not fit (e.g. a fractional
// move coordinate). Unknown message types are *not* an error here — callers
// ignore them per PROTOCOL §0.
func ParseInbound(data []byte) (Inbound, error) {
	var in Inbound
	if err := json.Unmarshal(data, &in); err != nil {
		return Inbound{}, err
	}
	if in.Type == "" {
		return Inbound{}, errMissingType
	}
	return in, nil
}

type protocolError string

func (e protocolError) Error() string { return string(e) }

var errMissingType = protocolError("missing message type")

// Marshal encodes an outbound message. Messages are small (<= ~2KB); a
// marshal failure would indicate a programming error, so the helper panics
// rather than silently dropping protocol frames.
func Marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("protocol: marshal outbound message: " + err.Error())
	}
	return b
}
