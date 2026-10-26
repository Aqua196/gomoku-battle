package cluster

import (
	"slices"
	"sync"
	"time"
)

// Directory is the shared state of a cluster. Every method is safe for
// concurrent use from any node; the multi-key ones (ClaimSeat, Enqueue,
// CloseWaiting) are atomic. Implementations never return transport
// errors: a failed read is a miss and a failed write is logged, matching
// store.Store, so a Redis hiccup degrades a feature instead of crashing.
type Directory interface {
	// SetLocation records that pid's socket now lives on node and returns
	// a fresh, increasing epoch for that socket plus the node that held the
	// player before ("" if none).
	SetLocation(pid, node string) (epoch int64, prev string)
	// ClearLocation forgets pid's location if it is still (node, epoch).
	ClearLocation(pid, node string, epoch int64)
	// Location reports where pid's socket lives.
	Location(pid string) (string, bool)

	// BindPlayer admits pid into roomID unless pid is already in a room.
	BindPlayer(pid, roomID string) bool
	// UnbindPlayer releases pid if it is bound to roomID.
	UnbindPlayer(pid, roomID string)
	// PlayerRoom reports the room pid is bound to.
	PlayerRoom(pid string) (string, bool)

	PutRoom(r RoomInfo)
	DeleteRoom(id string)
	Room(id string) (RoomInfo, bool)
	Rooms() []RoomInfo
	// ClaimSeat seats s as white in a waiting room, flips it to playing
	// and binds s.PlayerID to it, all or nothing.
	ClaimSeat(roomID string, s SeatInfo) (RoomInfo, error)
	// CloseWaiting deletes the room only while it is still waiting (a join
	// may have just claimed it) and unbinds its players; false if not.
	CloseWaiting(roomID string) bool

	// Enqueue appends e to the FIFO queue (a second call for the same
	// player keeps its place). When two players are queued, the oldest
	// two are removed, bound to roomID and returned as the pair.
	// Otherwise the 1-based position of e is returned.
	Enqueue(e QueueEntry, roomID string) (pair []QueueEntry, position int)
	// Dequeue removes pid from the queue, reporting whether it was there.
	Dequeue(pid string) bool
	// Queue lists the queue, oldest first.
	Queue() []QueueEntry

	SaveLive(g LiveGame)
	DeleteLive(roomID string)
	LiveGames() []LiveGame

	// AcquireLease takes roomID's lease for node unless another node holds
	// it; RenewLease extends it only while node still holds it.
	AcquireLease(roomID, node string, ttl time.Duration) bool
	RenewLease(roomID, node string, ttl time.Duration) bool
	ReleaseLease(roomID, node string)
	LeaseHolder(roomID string) (string, bool)

	// Heartbeat marks node alive for ttl; Alive reports it.
	Heartbeat(node string, ttl time.Duration)
	Alive(node string) bool
}

// MemDirectory is the single-process Directory: one mutex, plain maps.
// Several hubs in one process may share it (tests run small clusters so).
type MemDirectory struct {
	mu     sync.Mutex
	now    func() time.Time
	loc    map[string]location
	epochs map[string]int64
	bound  map[string]string
	rooms  map[string]RoomInfo
	queue  []QueueEntry
	live   map[string]LiveGame
	leases map[string]lease
	beats  map[string]time.Time
}

type location struct {
	node  string
	epoch int64
}

type lease struct {
	node    string
	expires time.Time
}

// NewMemDirectory returns an empty in-memory directory.
func NewMemDirectory() *MemDirectory {
	return &MemDirectory{
		now:    time.Now,
		loc:    make(map[string]location),
		epochs: make(map[string]int64),
		bound:  make(map[string]string),
		rooms:  make(map[string]RoomInfo),
		live:   make(map[string]LiveGame),
		leases: make(map[string]lease),
		beats:  make(map[string]time.Time),
	}
}

func (d *MemDirectory) SetLocation(pid, node string) (int64, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.epochs[pid]++
	prev := d.loc[pid].node
	d.loc[pid] = location{node: node, epoch: d.epochs[pid]}
	return d.epochs[pid], prev
}

func (d *MemDirectory) ClearLocation(pid, node string, epoch int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.loc[pid]; l.node == node && l.epoch == epoch {
		delete(d.loc, pid)
	}
}

func (d *MemDirectory) Location(pid string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l, ok := d.loc[pid]
	return l.node, ok
}

func (d *MemDirectory) BindPlayer(pid, roomID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, taken := d.bound[pid]; taken {
		return false
	}
	d.bound[pid] = roomID
	return true
}

func (d *MemDirectory) UnbindPlayer(pid, roomID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.bound[pid] == roomID {
		delete(d.bound, pid)
	}
}

func (d *MemDirectory) PlayerRoom(pid string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.bound[pid]
	return r, ok
}

func (d *MemDirectory) PutRoom(r RoomInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rooms[r.ID] = cloneRoom(r)
}

func (d *MemDirectory) DeleteRoom(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.rooms, id)
}

func (d *MemDirectory) Room(id string) (RoomInfo, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.rooms[id]
	return cloneRoom(r), ok
}

func (d *MemDirectory) Rooms() []RoomInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]RoomInfo, 0, len(d.rooms))
	for _, r := range d.rooms {
		out = append(out, cloneRoom(r))
	}
	return out
}

func (d *MemDirectory) ClaimSeat(roomID string, s SeatInfo) (RoomInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.rooms[roomID]
	switch {
	case !ok:
		return RoomInfo{}, ErrNoRoom
	case r.Status != StatusWaiting || r.Seats[1] != nil:
		return RoomInfo{}, ErrRoomFull
	}
	if _, taken := d.bound[s.PlayerID]; taken {
		return RoomInfo{}, ErrBound
	}
	r = cloneRoom(r)
	r.Seats[1] = &s
	r.Status = StatusPlaying
	d.rooms[roomID] = r
	d.bound[s.PlayerID] = roomID
	return cloneRoom(r), nil
}

func (d *MemDirectory) CloseWaiting(roomID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.rooms[roomID]
	if !ok || r.Status != StatusWaiting {
		return false
	}
	delete(d.rooms, roomID)
	for _, s := range r.Seats {
		if s != nil && d.bound[s.PlayerID] == roomID {
			delete(d.bound, s.PlayerID)
		}
	}
	return true
}

func (d *MemDirectory) Enqueue(e QueueEntry, roomID string) ([]QueueEntry, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !slices.ContainsFunc(d.queue, func(q QueueEntry) bool { return q.PlayerID == e.PlayerID }) {
		d.queue = append(d.queue, e)
	}
	// Entries whose player got into a room meanwhile are stale: drop them.
	d.queue = slices.DeleteFunc(d.queue, func(q QueueEntry) bool {
		_, taken := d.bound[q.PlayerID]
		return taken
	})
	if len(d.queue) >= 2 {
		pair := []QueueEntry{d.queue[0], d.queue[1]}
		d.queue = append([]QueueEntry(nil), d.queue[2:]...)
		d.bound[pair[0].PlayerID] = roomID
		d.bound[pair[1].PlayerID] = roomID
		return pair, 0
	}
	return nil, slices.IndexFunc(d.queue, func(q QueueEntry) bool { return q.PlayerID == e.PlayerID }) + 1
}

func (d *MemDirectory) Dequeue(pid string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.queue)
	d.queue = slices.DeleteFunc(d.queue, func(q QueueEntry) bool { return q.PlayerID == pid })
	return len(d.queue) != n
}

func (d *MemDirectory) Queue() []QueueEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.queue)
}

func (d *MemDirectory) SaveLive(g LiveGame) {
	g.Moves = slices.Clone(g.Moves)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.live[g.RoomID] = g
}

func (d *MemDirectory) DeleteLive(roomID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.live, roomID)
}

func (d *MemDirectory) LiveGames() []LiveGame {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]LiveGame, 0, len(d.live))
	for _, g := range d.live {
		g.Moves = slices.Clone(g.Moves)
		out = append(out, g)
	}
	return out
}

func (d *MemDirectory) AcquireLease(roomID, node string, ttl time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l, ok := d.leases[roomID]; ok && l.node != node && d.now().Before(l.expires) {
		return false
	}
	d.leases[roomID] = lease{node: node, expires: d.now().Add(ttl)}
	return true
}

func (d *MemDirectory) RenewLease(roomID, node string, ttl time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l, ok := d.leases[roomID]; !ok || l.node != node || !d.now().Before(l.expires) {
		return false
	}
	d.leases[roomID] = lease{node: node, expires: d.now().Add(ttl)}
	return true
}

func (d *MemDirectory) ReleaseLease(roomID, node string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.leases[roomID].node == node {
		delete(d.leases, roomID)
	}
}

func (d *MemDirectory) LeaseHolder(roomID string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l, ok := d.leases[roomID]
	if !ok || !d.now().Before(l.expires) {
		return "", false
	}
	return l.node, true
}

func (d *MemDirectory) Heartbeat(node string, ttl time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.beats[node] = d.now().Add(ttl)
}

func (d *MemDirectory) Alive(node string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.now().Before(d.beats[node])
}

// Room statuses as stored in RoomInfo (same literals as the protocol).
const (
	StatusWaiting = "waiting"
	StatusPlaying = "playing"
)

// Clone deep-copies the seat pointers so the copy shares nothing.
func (r RoomInfo) Clone() RoomInfo { return cloneRoom(r) }

// cloneRoom deep-copies the seat pointers so callers never share them.
func cloneRoom(r RoomInfo) RoomInfo {
	for i, s := range r.Seats {
		if s != nil {
			c := *s
			r.Seats[i] = &c
		}
	}
	return r
}
