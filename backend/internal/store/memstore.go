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
	rooms       map[string]RoomRecord
}

func NewMemStore() *MemStore {
	return &MemStore{
		playersByID: make(map[string]PlayerRecord),
		playerIDByT: make(map[string]string),
		rooms:       make(map[string]RoomRecord),
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

func (s *MemStore) UpsertRoom(r RoomRecord) {
	s.mu.Lock()
	s.rooms[r.ID] = r
	s.mu.Unlock()
}

func (s *MemStore) RoomByID(id string) (RoomRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rooms[id]
	return r, ok
}

func (s *MemStore) DeleteRoom(id string) {
	s.mu.Lock()
	delete(s.rooms, id)
	s.mu.Unlock()
}

func (s *MemStore) ListRooms() []RoomRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RoomRecord, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r)
	}
	return out
}

// randToken returns n cryptographically random bytes, base64url-encoded.
func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
