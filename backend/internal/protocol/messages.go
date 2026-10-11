package protocol

// Outbound message shapes (server -> client), field-for-field from
// PROTOCOL.md v1. Nullable JSON fields (winner, deadlines, lastMove, color)
// are pointers so they serialize as null instead of a sentinel value.

// WelcomeMsg is sent once after a successful hello. Resumed=true means the
// token bound this connection to a previously disconnected session.
type WelcomeMsg struct {
	Type     string `json:"type"`
	PlayerID string `json:"playerId"`
	Token    string `json:"token"`
	Nick     string `json:"nick"`
	Resumed  bool   `json:"resumed"`
}

func NewWelcome(playerID, token, nick string, resumed bool) *WelcomeMsg {
	return &WelcomeMsg{Type: TWelcome, PlayerID: playerID, Token: token, Nick: nick, Resumed: resumed}
}

// PongMsg answers an application-level ping; ts echoes the client ts when
// provided, else the server's current time (PROTOCOL §1 allows either).
type PongMsg struct {
	Type string `json:"type"`
	TS   int64  `json:"ts"`
}

// ErrorMsg is sent only to the offending connection.
type ErrorMsg struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewError(code, message string) *ErrorMsg {
	return &ErrorMsg{Type: TError, Code: code, Message: message}
}

// RoomPlayer is a seated player inside a Room. Color may be null when a seat
// has no assigned color (PROTOCOL §2 Room shape allows "null").
type RoomPlayer struct {
	PlayerID  string  `json:"playerId"`
	Nick      string  `json:"nick"`
	Color     *string `json:"color"`
	Connected bool    `json:"connected"`
}

// Room is the lobby-facing room record.
type Room struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    string       `json:"status"` // waiting|playing|finished
	Players   []RoomPlayer `json:"players"`
	IsAI      bool         `json:"isAI"`
	CreatedAt int64        `json:"createdAt"`
}

// RoomMsg carries room lifecycle events (created/joined/left).
type RoomMsg struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	Room   *Room  `json:"room,omitempty"`
	RoomID string `json:"roomId,omitempty"`
}

func NewRoomAction(action string, room *Room) *RoomMsg {
	return &RoomMsg{Type: TRoom, Action: action, Room: room}
}

func NewRoomLeft(roomID string) *RoomMsg {
	return &RoomMsg{Type: TRoom, Action: RoomLeft, RoomID: roomID}
}

// RoomListMsg is the full room snapshot pushed to lobby players or returned
// for an explicit list_rooms request.
type RoomListMsg struct {
	Type  string `json:"type"`
	Rooms []Room `json:"rooms"`
}

// MatchmakingMsg reports queue state. Position is 1-based and omitted when
// not queued.
type MatchmakingMsg struct {
	Type     string `json:"type"`
	Status   string `json:"status"` // queued|cancelled
	Position int    `json:"position,omitempty"`
}

func NewMatchmaking(status string, position int) *MatchmakingMsg {
	return &MatchmakingMsg{Type: TMatchmaking, Status: status, Position: position}
}

// Pos is a board coordinate.
type Pos struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// GamePlayer is one side of a GameState.
type GamePlayer struct {
	PlayerID  string `json:"playerId"`
	Nick      string `json:"nick"`
	Connected bool   `json:"connected"`
	IsAI      bool   `json:"isAI"`
}

// GamePlayers indexes the two seats by color.
type GamePlayers struct {
	Black GamePlayer `json:"black"`
	White GamePlayer `json:"white"`
}

// GameState is the authoritative full-state snapshot (PROTOCOL §3).
type GameState struct {
	RoomID            string      `json:"roomId"`
	Status            string      `json:"status"` // playing|finished
	Board             [][]int     `json:"board"`
	Turn              string      `json:"turn"` // black|white
	MoveNumber        int         `json:"moveNumber"`
	LastMove          *Pos        `json:"lastMove"`
	TurnDeadline      *int64      `json:"turnDeadline"`
	ReconnectDeadline *int64      `json:"reconnectDeadline"`
	Players           GamePlayers `json:"players"`
	Winner            *string     `json:"winner"`
	WinReason         *string     `json:"winReason"`
}

// GameStartMsg opens a game; YouColor is personalized per recipient.
type GameStartMsg struct {
	Type     string     `json:"type"`
	Game     *GameState `json:"game"`
	YouColor string     `json:"youColor"`
}

// GameStateMsg is broadcast after every legal move.
type GameStateMsg struct {
	Type string     `json:"type"`
	Game *GameState `json:"game"`
}

// GameOverMsg ends a game. Winner is null only for a draw.
type GameOverMsg struct {
	Type   string     `json:"type"`
	Reason string     `json:"reason"`
	Winner *string    `json:"winner"`
	Game   *GameState `json:"game"`
}

// OpponentPresenceMsg reports the other side's connectivity. Deadline is the
// reconnect-forfeit timestamp when connected=false, else null.
type OpponentPresenceMsg struct {
	Type      string `json:"type"`
	Connected bool   `json:"connected"`
	Deadline  *int64 `json:"deadline"`
}

// StrPtr returns a pointer to s (for nullable color fields).
func StrPtr(s string) *string { return &s }

// Int64Ptr returns a pointer to v (for nullable deadline fields).
func Int64Ptr(v int64) *int64 { return &v }
