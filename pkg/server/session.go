package server

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type Session struct {
	Token     string
	Email     string
	CreatedAt time.Time
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

func (ss *SessionStore) Create(email string) string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	token := "sess_" + hex.EncodeToString(buf)

	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sessions[token] = &Session{
		Token:     token,
		Email:     email,
		CreatedAt: time.Now(),
	}
	return token
}

func (ss *SessionStore) Validate(token string) (string, bool) {
	ss.mu.RLock()
	s, ok := ss.sessions[token]
	ss.mu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Since(s.CreatedAt) > 24*time.Hour {
		ss.mu.Lock()
		delete(ss.sessions, token)
		ss.mu.Unlock()
		return "", false
	}
	return s.Email, true
}

func (ss *SessionStore) Revoke(token string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.sessions, token)
}
