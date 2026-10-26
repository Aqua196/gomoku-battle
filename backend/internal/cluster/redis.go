package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisDirectory keeps the Directory in Redis under "c:" keys. Single-key
// writes are plain commands; compare-and-set ones are short Lua scripts;
// ClaimSeat and CloseWaiting (one room each, little contention)
// read-decide-write under WATCH and retry on conflict; the queue is a script. Every call is bounded by opTimeout.
type RedisDirectory struct {
	rdb *redis.Client
}

const (
	opTimeout  = 300 * time.Millisecond
	recordTTL  = 24 * time.Hour // safety net for keys whose owner vanished
	txAttempts = 8
)

// NewRedisDirectory uses an existing client (the server shares one with
// the store).
func NewRedisDirectory(rdb *redis.Client) *RedisDirectory { return &RedisDirectory{rdb: rdb} }

func op() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

func warn(what string, err error) {
	if err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("cluster: redis "+what, "err", err)
	}
}

func kLoc(pid string) string      { return "c:loc:" + pid }
func kEpoch(pid string) string    { return "c:epoch:" + pid }
func kBind(pid string) string     { return "c:bind:" + pid }
func kRoom(id string) string      { return "c:room:" + id }
func kLive(id string) string      { return "c:live:" + id }
func kLease(id string) string     { return "c:lease:" + id }
func kNode(node string) string    { return "c:node:" + node }
func ttlMs(d time.Duration) int64 { return d.Milliseconds() }

const (
	kRooms = "c:rooms"
	kLives = "c:lives"
	kQueue = "c:queue"
)

var (
	setLocation = redis.NewScript(`
local e = redis.call('INCR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
local prev = redis.call('GET', KEYS[2])
redis.call('SET', KEYS[2], ARGV[1] .. '|' .. e, 'PX', ARGV[2])
return {e, prev or ''}`)
	delIfEquals = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)
	acquireLease = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur and cur ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1`)
	renewLease = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
return 0`)
)

func (d *RedisDirectory) SetLocation(pid, node string) (int64, string) {
	ctx, cancel := op()
	defer cancel()
	res, err := setLocation.Run(ctx, d.rdb, []string{kEpoch(pid), kLoc(pid)}, node, ttlMs(recordTTL)).Slice()
	if err != nil || len(res) != 2 {
		warn("set location", err)
		return 0, ""
	}
	epoch, _ := res[0].(int64)
	prev, _ := res[1].(string)
	prevNode, _, _ := strings.Cut(prev, "|")
	return epoch, prevNode
}

func (d *RedisDirectory) ClearLocation(pid, node string, epoch int64) {
	ctx, cancel := op()
	defer cancel()
	warn("clear location", delIfEquals.Run(ctx, d.rdb, []string{kLoc(pid)}, node+"|"+strconv.FormatInt(epoch, 10)).Err())
}

func (d *RedisDirectory) Location(pid string) (string, bool) {
	ctx, cancel := op()
	defer cancel()
	v, err := d.rdb.Get(ctx, kLoc(pid)).Result()
	if err != nil {
		warn("location", err)
		return "", false
	}
	node, _, _ := strings.Cut(v, "|")
	return node, true
}

func (d *RedisDirectory) BindPlayer(pid, roomID string) bool {
	ctx, cancel := op()
	defer cancel()
	ok, err := d.rdb.SetNX(ctx, kBind(pid), roomID, recordTTL).Result()
	warn("bind", err)
	return ok
}

func (d *RedisDirectory) UnbindPlayer(pid, roomID string) {
	ctx, cancel := op()
	defer cancel()
	warn("unbind", delIfEquals.Run(ctx, d.rdb, []string{kBind(pid)}, roomID).Err())
}

func (d *RedisDirectory) PlayerRoom(pid string) (string, bool) {
	ctx, cancel := op()
	defer cancel()
	v, err := d.rdb.Get(ctx, kBind(pid)).Result()
	warn("player room", err)
	return v, err == nil
}

func (d *RedisDirectory) PutRoom(r RoomInfo) {
	data, _ := json.Marshal(r)
	ctx, cancel := op()
	defer cancel()
	pipe := d.rdb.TxPipeline()
	pipe.Set(ctx, kRoom(r.ID), data, recordTTL)
	pipe.SAdd(ctx, kRooms, r.ID)
	_, err := pipe.Exec(ctx)
	warn("put room", err)
}

func (d *RedisDirectory) DeleteRoom(id string) {
	ctx, cancel := op()
	defer cancel()
	pipe := d.rdb.TxPipeline()
	pipe.Del(ctx, kRoom(id))
	pipe.SRem(ctx, kRooms, id)
	_, err := pipe.Exec(ctx)
	warn("delete room", err)
}

func (d *RedisDirectory) Room(id string) (RoomInfo, bool) {
	ctx, cancel := op()
	defer cancel()
	return getJSON[RoomInfo](ctx, d.rdb, kRoom(id))
}

func (d *RedisDirectory) Rooms() []RoomInfo {
	ctx, cancel := op()
	defer cancel()
	return listJSON[RoomInfo](ctx, d.rdb, kRooms, kRoom)
}

func (d *RedisDirectory) ClaimSeat(roomID string, s SeatInfo) (RoomInfo, error) {
	var out RoomInfo
	var verdict error
	err := d.watch(func(ctx context.Context, tx *redis.Tx) error {
		r, ok := getJSON[RoomInfo](ctx, tx, kRoom(roomID))
		switch {
		case !ok:
			verdict = ErrNoRoom
			return nil
		case r.Status != StatusWaiting || r.Seats[1] != nil:
			verdict = ErrRoomFull
			return nil
		}
		if n, err := tx.Exists(ctx, kBind(s.PlayerID)).Result(); err != nil || n > 0 {
			verdict = ErrBound
			return err
		}
		r.Seats[1] = &s
		r.Status = StatusPlaying
		data, _ := json.Marshal(r)
		_, err := tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, kRoom(roomID), data, recordTTL)
			p.Set(ctx, kBind(s.PlayerID), roomID, recordTTL)
			return nil
		})
		out, verdict = r, nil
		return err
	}, kRoom(roomID), kBind(s.PlayerID))
	if err != nil {
		warn("claim seat", err)
		return RoomInfo{}, ErrNoRoom
	}
	return out, verdict
}

func (d *RedisDirectory) CloseWaiting(roomID string) bool {
	closed := false
	err := d.watch(func(ctx context.Context, tx *redis.Tx) error {
		r, ok := getJSON[RoomInfo](ctx, tx, kRoom(roomID))
		if !ok || r.Status != StatusWaiting {
			return nil
		}
		_, err := tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Del(ctx, kRoom(roomID))
			p.SRem(ctx, kRooms, roomID)
			for _, s := range r.Seats {
				if s != nil {
					delIfEquals.Eval(ctx, p, []string{kBind(s.PlayerID)}, roomID)
				}
			}
			return nil
		})
		closed = err == nil
		return err
	}, kRoom(roomID))
	warn("close waiting", err)
	return closed
}

// Queue entries are stored as "pid\tnode\tnick" so the Lua below can
// read the player id without a JSON decoder.
func encodeEntry(q QueueEntry) string { return q.PlayerID + "\t" + q.Node + "\t" + q.Nick }

func decodeEntry(s string) (QueueEntry, bool) {
	parts := strings.SplitN(s, "\t", 3)
	if len(parts) != 3 {
		return QueueEntry{}, false
	}
	return QueueEntry{PlayerID: parts[0], Node: parts[1], Nick: parts[2]}, true
}

// enqueue runs as one atomic script: matchmaking is the hottest contended
// key, where optimistic WATCH transactions would keep retrying. It drops
// entries whose player got into a room meanwhile, appends the caller
// (unless queued or bound already), pairs and binds the oldest two.
// (Bind keys are computed in the script, so this assumes a single Redis,
// not Redis Cluster.)
var enqueueScript = redis.NewScript(`
local function pid(v) return string.match(v, '^([^\t]*)') end
local keep, mine = {}, false
for _, v in ipairs(redis.call('LRANGE', KEYS[1], 0, -1)) do
  if redis.call('EXISTS', ARGV[4] .. pid(v)) == 0 then
    table.insert(keep, v)
    if pid(v) == ARGV[2] then mine = true end
  end
end
if not mine and redis.call('EXISTS', ARGV[4] .. ARGV[2]) == 0 then table.insert(keep, ARGV[1]) end
local a, b, pos = '', '', 0
if #keep >= 2 then
  a = table.remove(keep, 1)
  b = table.remove(keep, 1)
  redis.call('SET', ARGV[4] .. pid(a), ARGV[3], 'PX', ARGV[5])
  redis.call('SET', ARGV[4] .. pid(b), ARGV[3], 'PX', ARGV[5])
else
  for i, v in ipairs(keep) do if pid(v) == ARGV[2] then pos = i end end
end
redis.call('DEL', KEYS[1])
if #keep > 0 then redis.call('RPUSH', KEYS[1], unpack(keep)) end
return {pos, a, b}`)

var dequeueScript = redis.NewScript(`
local n = 0
for _, v in ipairs(redis.call('LRANGE', KEYS[1], 0, -1)) do
  if string.match(v, '^([^\t]*)') == ARGV[1] then n = n + redis.call('LREM', KEYS[1], 0, v) end
end
return n`)

func (d *RedisDirectory) Enqueue(e QueueEntry, roomID string) ([]QueueEntry, int) {
	ctx, cancel := op()
	defer cancel()
	res, err := enqueueScript.Run(ctx, d.rdb, []string{kQueue},
		encodeEntry(e), e.PlayerID, roomID, kBind(""), ttlMs(recordTTL)).Slice()
	if err != nil || len(res) != 3 {
		warn("enqueue", err)
		return nil, 0
	}
	a, _ := res[1].(string)
	b, _ := res[2].(string)
	if a != "" && b != "" {
		qa, _ := decodeEntry(a)
		qb, _ := decodeEntry(b)
		return []QueueEntry{qa, qb}, 0
	}
	pos, _ := res[0].(int64)
	return nil, int(pos)
}

func (d *RedisDirectory) Dequeue(pid string) bool {
	ctx, cancel := op()
	defer cancel()
	n, err := dequeueScript.Run(ctx, d.rdb, []string{kQueue}, pid).Int()
	warn("dequeue", err)
	return n > 0
}

func (d *RedisDirectory) Queue() []QueueEntry {
	ctx, cancel := op()
	defer cancel()
	raw, err := d.rdb.LRange(ctx, kQueue, 0, -1).Result()
	warn("queue", err)
	out := make([]QueueEntry, 0, len(raw))
	for _, s := range raw {
		if q, ok := decodeEntry(s); ok {
			out = append(out, q)
		}
	}
	return out
}

func (d *RedisDirectory) SaveLive(g LiveGame) {
	data, _ := json.Marshal(g)
	ctx, cancel := op()
	defer cancel()
	pipe := d.rdb.TxPipeline()
	pipe.Set(ctx, kLive(g.RoomID), data, recordTTL)
	pipe.SAdd(ctx, kLives, g.RoomID)
	_, err := pipe.Exec(ctx)
	warn("save live", err)
}

func (d *RedisDirectory) DeleteLive(roomID string) {
	ctx, cancel := op()
	defer cancel()
	pipe := d.rdb.TxPipeline()
	pipe.Del(ctx, kLive(roomID))
	pipe.SRem(ctx, kLives, roomID)
	_, err := pipe.Exec(ctx)
	warn("delete live", err)
}

func (d *RedisDirectory) LiveGames() []LiveGame {
	ctx, cancel := op()
	defer cancel()
	return listJSON[LiveGame](ctx, d.rdb, kLives, kLive)
}

func (d *RedisDirectory) AcquireLease(roomID, node string, ttl time.Duration) bool {
	ctx, cancel := op()
	defer cancel()
	n, err := acquireLease.Run(ctx, d.rdb, []string{kLease(roomID)}, node, ttlMs(ttl)).Int()
	warn("acquire lease", err)
	return n == 1
}

func (d *RedisDirectory) RenewLease(roomID, node string, ttl time.Duration) bool {
	ctx, cancel := op()
	defer cancel()
	n, err := renewLease.Run(ctx, d.rdb, []string{kLease(roomID)}, node, ttlMs(ttl)).Int()
	warn("renew lease", err)
	return n == 1
}

func (d *RedisDirectory) ReleaseLease(roomID, node string) {
	ctx, cancel := op()
	defer cancel()
	warn("release lease", delIfEquals.Run(ctx, d.rdb, []string{kLease(roomID)}, node).Err())
}

func (d *RedisDirectory) LeaseHolder(roomID string) (string, bool) {
	ctx, cancel := op()
	defer cancel()
	v, err := d.rdb.Get(ctx, kLease(roomID)).Result()
	warn("lease holder", err)
	return v, err == nil
}

func (d *RedisDirectory) Heartbeat(node string, ttl time.Duration) {
	ctx, cancel := op()
	defer cancel()
	warn("heartbeat", d.rdb.Set(ctx, kNode(node), "1", ttl).Err())
}

func (d *RedisDirectory) Alive(node string) bool {
	ctx, cancel := op()
	defer cancel()
	n, err := d.rdb.Exists(ctx, kNode(node)).Result()
	if err != nil {
		warn("alive", err)
		return true // unknown: never declare a node dead on a Redis error
	}
	return n == 1
}

// watch runs fn as an optimistic transaction over keys, retrying when a
// watched key changed under it.
func (d *RedisDirectory) watch(fn func(context.Context, *redis.Tx) error, keys ...string) error {
	var err error
	for range txAttempts {
		ctx, cancel := op()
		err = d.rdb.Watch(ctx, func(tx *redis.Tx) error { return fn(ctx, tx) }, keys...)
		cancel()
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return err
}

func getJSON[T any](ctx context.Context, c redis.Cmdable, key string) (T, bool) {
	var v T
	data, err := c.Get(ctx, key).Bytes()
	if err != nil {
		warn("get "+key, err)
		return v, false
	}
	if err := json.Unmarshal(data, &v); err != nil {
		warn("decode "+key, err)
		return v, false
	}
	return v, true
}

// listJSON reads every record named in an index set in one pipeline and
// sweeps ids whose record has expired out of the index.
func listJSON[T any](ctx context.Context, rdb *redis.Client, index string, key func(string) string) []T {
	ids, err := rdb.SMembers(ctx, index).Result()
	if err != nil || len(ids) == 0 {
		warn("list "+index, err)
		return nil
	}
	pipe := rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Get(ctx, key(id))
	}
	_, err = pipe.Exec(ctx)
	warn("list "+index, err)
	out := make([]T, 0, len(ids))
	var dead []any
	for i, c := range cmds {
		data, err := c.Bytes()
		if errors.Is(err, redis.Nil) {
			dead = append(dead, ids[i])
			continue
		}
		var v T
		if err == nil && json.Unmarshal(data, &v) == nil {
			out = append(out, v)
		}
	}
	if len(dead) > 0 {
		warn("sweep "+index, rdb.SRem(ctx, index, dead...).Err())
	}
	return out
}
