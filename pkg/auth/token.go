package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type TokenEntry struct {
	Token string `json:"token"`
	Label string `json:"label"`
}

type TokenStore struct {
	tokens   map[string]string
	filePath string
	mu       sync.RWMutex
}

type tokenFile struct {
	Tokens map[string]string `yaml:"tokens"`
}

func NewTokenStore() *TokenStore {
	return &TokenStore{
		tokens: make(map[string]string),
	}
}

func (s *TokenStore) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read token file: %w", err)
	}

	var tf tokenFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return fmt.Errorf("failed to parse token file: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = tf.Tokens
	s.filePath = path
	if s.tokens == nil {
		s.tokens = make(map[string]string)
	}
	return nil
}

func (s *TokenStore) Add(token, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = label
}

// Generate creates a unique token with sk_ prefix and persists to file.
func (s *TokenStore) Generate(label string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	token := "sk_" + hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.filePath == "" {
		return "", fmt.Errorf("no token file configured, cannot persist new tokens")
	}

	s.tokens[token] = label
	if err := s.persistLocked(); err != nil {
		delete(s.tokens, token)
		return "", err
	}
	return token, nil
}

func (s *TokenStore) List() []TokenEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := make([]TokenEntry, 0, len(s.tokens))
	for token, label := range s.tokens {
		entries = append(entries, TokenEntry{Token: token, Label: label})
	}
	return entries
}

func (s *TokenStore) Remove(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	label, ok := s.tokens[token]
	if !ok {
		return false
	}
	delete(s.tokens, token)
	if s.filePath != "" {
		if err := s.persistLocked(); err != nil {
			s.tokens[token] = label // restore on write failure
			return false
		}
	}
	return true
}

func (s *TokenStore) Persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

func (s *TokenStore) persistLocked() error {
	if s.filePath == "" {
		return nil
	}
	tf := tokenFile{Tokens: s.tokens}
	data, err := yaml.Marshal(&tf)
	if err != nil {
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}
	return nil
}

func (s *TokenStore) Validate(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	label, ok := s.tokens[token]
	return label, ok
}

func (s *TokenStore) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens) > 0
}
