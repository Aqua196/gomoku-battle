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
	EBadRequest        = "bad_request"
	EHelloTimeout      = "hello_timeout"
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
	ENotImplemented    = "not_implemented"
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
