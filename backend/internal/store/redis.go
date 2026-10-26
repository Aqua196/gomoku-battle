// Package store — RedisStore is the Store interface over Redis (M4):
// sessions survive process restart (24h TTL, sliding on every read), the
// room registry is durable-ish metadata (1h TTL; the hub deletes rooms on
// game over anyway). Records are JSON values with token->id and
// id->token secondary indexes; every miss or transport error degrades to
// "not found" and logs a warning, matching the interface's error-free
// contract. Every operation runs under a bounded timeout so a slow or
// dead Redis cannot freeze the hub: worst case per call is opTimeout,
// never an unbounded dial/retry wait.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	playerTTL = 24 * time.Hour
	roomTTL   = time.Hour
	opTimeout = 300 * time.Millisecond
)

type RedisStore struct {
	rdb *redis.Client
}

// NewRedisStore dials Redis and verifies connectivity with a ping, with a
// short retry window (~5s) so compose app+redis boot order is not racy.
// Callers fall back to MemStore on error.
func NewRedisStore(addr string) (*RedisStore, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	var last error
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		last = rdb.Ping(ctx).Err()
		cancel()
		if last == nil {
			return &RedisStore{rdb: rdb}, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = rdb.Close()
	return nil, last
}

func (s *RedisStore) op() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

func (s *RedisStore) playerKey(id string) string       { return "p:" + id }
func (s *RedisStore) playerTokenKey(t string) string   { return "pt:" + t }
func (s *RedisStore) playerTokIdxKey(id string) string { return "pti:" + id }
func (s *RedisStore) roomKey(id string) string         { return "r:" + id }

func (s *RedisStore) put(rec *PlayerRecord) {
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("store: marshal player", "err", err)
		return
	}
	ctx, cancel := s.op()
	defer cancel()
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, s.playerKey(rec.PlayerID), data, playerTTL)
	pipe.Set(ctx, s.playerTokenKey(rec.Token), rec.PlayerID, playerTTL)
	pipe.Set(ctx, s.playerTokIdxKey(rec.PlayerID), rec.Token, playerTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("store: redis write player", "err", err)
	}
}

func (s *RedisStore) CreatePlayer(nick string) *PlayerRecord {
	rec := &PlayerRecord{
		PlayerID: "p_" + randToken(8),
		Token:    randToken(32),
		Nick:     nick,
	}
	s.put(rec)
	return rec
}

// getPlayer reads a player record and slides its TTL forward (pipeline
// GET+EXPIRE) so an active player near the 24h edge is not evicted.
func (s *RedisStore) getPlayer(id string) (*PlayerRecord, bool) {
	ctx, cancel := s.op()
	defer cancel()
	pipe := s.rdb.TxPipeline()
	get := pipe.Get(ctx, s.playerKey(id))
	pipe.Expire(ctx, s.playerKey(id), playerTTL)
	pipe.Expire(ctx, s.playerTokIdxKey(id), playerTTL)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("store: redis get player", "err", err)
	}
	data, err := get.Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("store: redis get player", "err", err)
		}
		return nil, false
	}
	var rec PlayerRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		slog.Warn("store: unmarshal player", "err", err)
		return nil, false
	}
	return &rec, true
}

func (s *RedisStore) PlayerByToken(token string) (*PlayerRecord, bool) {
	ctx, cancel := s.op()
	defer cancel()
	pipe := s.rdb.TxPipeline()
	get := pipe.Get(ctx, s.playerTokenKey(token))
	pipe.Expire(ctx, s.playerTokenKey(token), playerTTL)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("store: redis get token", "err", err)
	}
	id, err := get.Result()
	if err != nil {
		return nil, false
	}
	return s.getPlayer(id)
}

func (s *RedisStore) PlayerByID(id string) (*PlayerRecord, bool) {
	return s.getPlayer(id)
}

func (s *RedisStore) UpdateNick(playerID, nick string) {
	rec, ok := s.getPlayer(playerID)
	if !ok {
		return
	}
	rec.Nick = nick
	s.put(rec)
}

// DeletePlayer resolves the token via the id->token index (a plain string,
// robust even when the player record itself is corrupt) and deletes all
// three keys so no orphan index entry survives.
func (s *RedisStore) DeletePlayer(playerID string) {
	ctx, cancel := s.op()
	defer cancel()
	tok, _ := s.rdb.Get(ctx, s.playerTokIdxKey(playerID)).Result()
	if tok == "" {
		if rec, ok := s.getPlayer(playerID); ok {
			tok = rec.Token
		}
	}
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, s.playerKey(playerID))
	pipe.Del(ctx, s.playerTokIdxKey(playerID))
	if tok != "" {
		pipe.Del(ctx, s.playerTokenKey(tok))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("store: redis del player", "err", err)
	}
}

func (s *RedisStore) UpsertRoom(r RoomRecord) {
	data, err := json.Marshal(r)
	if err != nil {
		slog.Warn("store: marshal room", "err", err)
		return
	}
	ctx, cancel := s.op()
	defer cancel()
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, s.roomKey(r.ID), data, roomTTL)
	pipe.SAdd(ctx, "rooms", r.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("store: redis upsert room", "err", err)
	}
}

func (s *RedisStore) RoomByID(id string) (RoomRecord, bool) {
	ctx, cancel := s.op()
	defer cancel()
	data, err := s.rdb.Get(ctx, s.roomKey(id)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("store: redis get room", "err", err)
		}
		return RoomRecord{}, false
	}
	var r RoomRecord
	if err := json.Unmarshal(data, &r); err != nil {
		slog.Warn("store: unmarshal room", "err", err)
		return RoomRecord{}, false
	}
	return r, true
}

func (s *RedisStore) DeleteRoom(id string) {
	ctx, cancel := s.op()
	defer cancel()
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, s.roomKey(id))
	pipe.SRem(ctx, "rooms", id)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("store: redis del room", "err", err)
	}
}

// ListRooms fetches all room records in one pipeline (no N+1 round trips)
// and sweeps dead ids out of the `rooms` set so expired rooms cannot leak
// permanently in the index.
func (s *RedisStore) ListRooms() []RoomRecord {
	ctx, cancel := s.op()
	defer cancel()
	ids, err := s.rdb.SMembers(ctx, "rooms").Result()
	if err != nil {
		slog.Warn("store: redis list rooms", "err", err)
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	pipe := s.rdb.TxPipeline()
	cmds := make(map[string]*redis.StringCmd, len(ids))
	for _, id := range ids {
		cmds[id] = pipe.Get(ctx, s.roomKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("store: redis list rooms", "err", err)
	}
	out := make([]RoomRecord, 0, len(ids))
	var dead []any
	for _, id := range ids {
		data, err := cmds[id].Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				dead = append(dead, id)
			}
			continue
		}
		var r RoomRecord
		if err := json.Unmarshal(data, &r); err != nil {
			slog.Warn("store: unmarshal room", "err", err)
			dead = append(dead, id)
			continue
		}
		out = append(out, r)
	}
	if len(dead) > 0 {
		if err := s.rdb.SRem(ctx, "rooms", dead...).Err(); err != nil {
			slog.Warn("store: redis sweep rooms", "err", err)
		}
	}
	return out
}
