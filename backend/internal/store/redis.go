// Package store — RedisStore is the Store interface over Redis (M4):
// sessions survive process restart (24h sliding TTL), the room registry is
// durable-ish metadata (1h TTL; the hub deletes rooms on game over anyway).
// Records are JSON values with a token->id secondary index; every miss or
// transport error degrades to "not found" and logs a warning, matching the
// interface's error-free contract. Operations issue a single pipeline per
// call where ordering allows; reads never mutate.
package store

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	playerTTL = 24 * time.Hour
	roomTTL   = time.Hour
)

type RedisStore struct {
	rdb *redis.Client
	ctx context.Context
}

// NewRedisStore dials Redis and verifies connectivity with a ping. Callers
// fall back to MemStore on error, so a RedisStore is only ever returned
// connected.
func NewRedisStore(addr string) (*RedisStore, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return &RedisStore{rdb: rdb, ctx: ctx}, nil
}

func (s *RedisStore) playerKey(id string) string     { return "p:" + id }
func (s *RedisStore) playerTokenKey(t string) string { return "pt:" + t }
func (s *RedisStore) roomKey(id string) string       { return "r:" + id }

func (s *RedisStore) put(rec *PlayerRecord) {
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("store: marshal player", "err", err)
		return
	}
	pipe := s.rdb.TxPipeline()
	pipe.Set(s.ctx, s.playerKey(rec.PlayerID), data, playerTTL)
	pipe.Set(s.ctx, s.playerTokenKey(rec.Token), rec.PlayerID, playerTTL)
	if _, err := pipe.Exec(s.ctx); err != nil {
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

func (s *RedisStore) getPlayer(id string) (*PlayerRecord, bool) {
	data, err := s.rdb.Get(s.ctx, s.playerKey(id)).Bytes()
	if err != nil {
		if err != redis.Nil {
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
	id, err := s.rdb.Get(s.ctx, s.playerTokenKey(token)).Result()
	if err != nil {
		if err != redis.Nil {
			slog.Warn("store: redis get token", "err", err)
		}
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

func (s *RedisStore) DeletePlayer(playerID string) {
	rec, ok := s.getPlayer(playerID)
	pipe := s.rdb.TxPipeline()
	pipe.Del(s.ctx, s.playerKey(playerID))
	if ok {
		pipe.Del(s.ctx, s.playerTokenKey(rec.Token))
	}
	if _, err := pipe.Exec(s.ctx); err != nil {
		slog.Warn("store: redis del player", "err", err)
	}
}

func (s *RedisStore) UpsertRoom(r RoomRecord) {
	data, err := json.Marshal(r)
	if err != nil {
		slog.Warn("store: marshal room", "err", err)
		return
	}
	pipe := s.rdb.TxPipeline()
	pipe.Set(s.ctx, s.roomKey(r.ID), data, roomTTL)
	pipe.SAdd(s.ctx, "rooms", r.ID)
	if _, err := pipe.Exec(s.ctx); err != nil {
		slog.Warn("store: redis upsert room", "err", err)
	}
}

func (s *RedisStore) RoomByID(id string) (RoomRecord, bool) {
	data, err := s.rdb.Get(s.ctx, s.roomKey(id)).Bytes()
	if err != nil {
		if err != redis.Nil {
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
	pipe := s.rdb.TxPipeline()
	pipe.Del(s.ctx, s.roomKey(id))
	pipe.SRem(s.ctx, "rooms", id)
	if _, err := pipe.Exec(s.ctx); err != nil {
		slog.Warn("store: redis del room", "err", err)
	}
}

func (s *RedisStore) ListRooms() []RoomRecord {
	ids, err := s.rdb.SMembers(s.ctx, "rooms").Result()
	if err != nil {
		slog.Warn("store: redis list rooms", "err", err)
		return nil
	}
	out := make([]RoomRecord, 0, len(ids))
	for _, id := range ids {
		if r, ok := s.RoomByID(id); ok {
			out = append(out, r)
		}
	}
	return out
}
