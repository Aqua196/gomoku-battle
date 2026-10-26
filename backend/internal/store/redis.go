// Package store — RedisStore is the Store interface over Redis (M4):
// sessions survive process restart (24h TTL, sliding on every read) and
// finished games are kept 30 days. Records are JSON values with token->id and
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
	gameTTL   = 30 * 24 * time.Hour
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
func (s *RedisStore) gameKey(id string) string         { return "g:" + id }
func (s *RedisStore) playerGamesKey(id string) string  { return "pg:" + id }

const recentGamesKey = "games:recent"

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

// SaveGame writes the record (30-day TTL) and indexes it in the global
// recent list and each human seat's list — sorted sets scored by end time,
// trimmed to the newest MaxRecentGames / MaxPlayerGames in the same
// transaction.
func (s *RedisStore) SaveGame(g GameRecord) {
	data, err := json.Marshal(g)
	if err != nil {
		slog.Warn("store: marshal game", "err", err)
		return
	}
	ctx, cancel := s.op()
	defer cancel()
	z := redis.Z{Score: float64(g.EndedAt), Member: g.ID}
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, s.gameKey(g.ID), data, gameTTL)
	pipe.ZAdd(ctx, recentGamesKey, z)
	pipe.ZRemRangeByRank(ctx, recentGamesKey, 0, -MaxRecentGames-1)
	for _, seat := range []SeatRecord{g.Black, g.White} {
		if seat.IsAI || seat.PlayerID == "" {
			continue
		}
		k := s.playerGamesKey(seat.PlayerID)
		pipe.ZAdd(ctx, k, z)
		pipe.ZRemRangeByRank(ctx, k, 0, -MaxPlayerGames-1)
		pipe.Expire(ctx, k, gameTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("store: redis save game", "err", err)
	}
}

func (s *RedisStore) GameByID(id string) (GameRecord, bool) {
	ctx, cancel := s.op()
	defer cancel()
	data, err := s.rdb.Get(ctx, s.gameKey(id)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("store: redis get game", "err", err)
		}
		return GameRecord{}, false
	}
	var g GameRecord
	if err := json.Unmarshal(data, &g); err != nil {
		slog.Warn("store: unmarshal game", "err", err)
		return GameRecord{}, false
	}
	return g, true
}

func (s *RedisStore) RecentGames(limit int) []GameRecord {
	return s.gamesIn(recentGamesKey, limit)
}

func (s *RedisStore) PlayerGames(playerID string, limit int) []GameRecord {
	return s.gamesIn(s.playerGamesKey(playerID), limit)
}

// gamesIn reads the newest limit ids of an index and their records in one
// pipeline; ids whose record expired are skipped.
func (s *RedisStore) gamesIn(index string, limit int) []GameRecord {
	if limit <= 0 {
		return nil
	}
	ctx, cancel := s.op()
	defer cancel()
	ids, err := s.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{Key: index, Start: 0, Stop: limit - 1, Rev: true}).Result()
	if err != nil {
		slog.Warn("store: redis list games", "err", err)
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Get(ctx, s.gameKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("store: redis list games", "err", err)
	}
	out := make([]GameRecord, 0, len(ids))
	for _, c := range cmds {
		data, err := c.Bytes()
		if err != nil {
			continue
		}
		var g GameRecord
		if json.Unmarshal(data, &g) == nil {
			out = append(out, g)
		}
	}
	return out
}
