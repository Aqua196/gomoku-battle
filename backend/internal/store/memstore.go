package store

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
)

// MemStore is the in-memory Store for M1. Sessions live as long as the
// process so a token stays resumable until restart (M4: RedisStore gives
// cross-restart resume). Records are stored by value and returned as fresh
// copies so callers never share mutable struct pointers with the store.
type MemStore struct {
	mu          sync.RWMutex
	playersByID map[string]PlayerRecord
	playerIDByT map[string]string // token -> playerID

	games       map[string]GameRecord
	recent      []string            // game ids, oldest first, capped at MaxRecentGames
	playerGames map[string][]string // player id -> game ids, oldest first, capped
}

func NewMemStore() *MemStore {
	return &MemStore{
		playersByID: make(map[string]PlayerRecord),
		playerIDByT: make(map[string]string),
		games:       make(map[string]GameRecord),
		playerGames: make(map[string][]string),
	}
}

func (s *MemStore) CreatePlayer(nick string) *PlayerRecord {
	rec := PlayerRecord{
		PlayerID: "p_" + randToken(8),
		Token:    randToken(32), // 256 bits of entropy, exceeds the >=16B floor
		Nick:     nick,
	}
	s.mu.Lock()
	s.playersByID[rec.PlayerID] = rec
	s.playerIDByT[rec.Token] = rec.PlayerID
	s.mu.Unlock()
	cp := rec
	return &cp
}

func (s *MemStore) PlayerByToken(token string) (*PlayerRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.playerIDByT[token]
	if !ok {
		return nil, false
	}
	rec, ok := s.playersByID[id]
	if !ok {
		return nil, false
	}
	cp := rec
	return &cp, true
}

func (s *MemStore) PlayerByID(id string) (*PlayerRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.playersByID[id]
	if !ok {
		return nil, false
	}
	cp := rec
	return &cp, true
}

func (s *MemStore) UpdateNick(playerID, nick string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.playersByID[playerID]; ok {
		rec.Nick = nick
		s.playersByID[playerID] = rec
	}
}

func (s *MemStore) DeletePlayer(playerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.playersByID[playerID]; ok {
		delete(s.playerIDByT, rec.Token)
		delete(s.playersByID, playerID)
	}
}

func (s *MemStore) SaveGame(g GameRecord) {
	g.Moves = cloneMoves(g.Moves)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.games[g.ID]; dup {
		s.games[g.ID] = g
		return
	}
	s.games[g.ID] = g
	s.recent = appendCapped(s.recent, g.ID, MaxRecentGames)
	for _, seat := range []SeatRecord{g.Black, g.White} {
		if seat.IsAI || seat.PlayerID == "" {
			continue
		}
		s.playerGames[seat.PlayerID] = appendCapped(s.playerGames[seat.PlayerID], g.ID, MaxPlayerGames)
	}
	s.collect()
}

// collect drops records that no index lists any more.
func (s *MemStore) collect() {
	if len(s.games) <= MaxRecentGames {
		return
	}
	live := make(map[string]bool, len(s.recent))
	for _, id := range s.recent {
		live[id] = true
	}
	for _, ids := range s.playerGames {
		for _, id := range ids {
			live[id] = true
		}
	}
	for id := range s.games {
		if !live[id] {
			delete(s.games, id)
		}
	}
}

func (s *MemStore) GameByID(id string) (GameRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.games[id]
	g.Moves = cloneMoves(g.Moves)
	return g, ok
}

func (s *MemStore) RecentGames(limit int) []GameRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newestLocked(s.recent, limit)
}

func (s *MemStore) PlayerGames(playerID string, limit int) []GameRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newestLocked(s.playerGames[playerID], limit)
}

func (s *MemStore) newestLocked(ids []string, limit int) []GameRecord {
	out := make([]GameRecord, 0, min(limit, len(ids)))
	for i := len(ids) - 1; i >= 0 && len(out) < limit; i-- {
		if g, ok := s.games[ids[i]]; ok {
			g.Moves = cloneMoves(g.Moves)
			out = append(out, g)
		}
	}
	return out
}

func appendCapped(ids []string, id string, limit int) []string {
	ids = append(ids, id)
	if len(ids) > limit {
		ids = append(ids[:0:0], ids[len(ids)-limit:]...)
	}
	return ids
}

func cloneMoves(m [][2]int) [][2]int {
	if m == nil {
		return nil
	}
	return append([][2]int(nil), m...)
}

// randToken returns n cryptographically random bytes, base64url-encoded.
func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
