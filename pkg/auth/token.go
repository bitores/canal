package auth

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type TokenStore struct {
	tokens map[string]string
	mu     sync.RWMutex
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
