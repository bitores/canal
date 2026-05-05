package auth

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type UserEntry struct {
	Email        string   `yaml:"email"`
	PasswordHash string   `yaml:"password_hash"`
	Tokens       []string `yaml:"tokens"`
	CreatedAt    string   `yaml:"created_at"`
	IsAdmin      bool     `yaml:"is_admin"`
}

type userFile struct {
	Users map[string]*UserEntry `yaml:"users"`
}

type UserStore struct {
	mu       sync.RWMutex
	users    map[string]*UserEntry
	filePath string
}

func NewUserStore() *UserStore {
	return &UserStore{
		users: make(map[string]*UserEntry),
	}
}

func (s *UserStore) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.filePath = path
			return nil
		}
		return fmt.Errorf("failed to read user file: %w", err)
	}

	var uf userFile
	if err := yaml.Unmarshal(data, &uf); err != nil {
		return fmt.Errorf("failed to parse user file: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = uf.Users
	s.filePath = path
	if s.users == nil {
		s.users = make(map[string]*UserEntry)
	}
	return nil
}

func (s *UserStore) CreateUser(email, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.filePath == "" {
		return fmt.Errorf("no user file configured")
	}
	if _, exists := s.users[email]; exists {
		return fmt.Errorf("user already exists")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	s.users[email] = &UserEntry{
		Email:        email,
		PasswordHash: string(hash),
		Tokens:       nil,
		CreatedAt:    time.Now().Format(time.RFC3339),
	}

	if err := s.persistLocked(); err != nil {
		delete(s.users, email)
		return err
	}
	return nil
}

func (s *UserStore) ValidatePassword(email, password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[email]
	if !ok {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

func (s *UserStore) AddToken(email, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[email]
	if !ok {
		return
	}
	u.Tokens = append(u.Tokens, token)
	_ = s.persistLocked()
}

func (s *UserStore) RemoveToken(email, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[email]
	if !ok {
		return false
	}
	found := false
	for i, t := range u.Tokens {
		if t == token {
			u.Tokens = append(u.Tokens[:i], u.Tokens[i+1:]...)
			found = true
			break
		}
	}
	if found {
		_ = s.persistLocked()
	}
	return found
}

func (s *UserStore) UserExists(email string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.users[email]
	return ok
}

func (s *UserStore) CreateAdmin(email, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.filePath == "" {
		return fmt.Errorf("no user file configured")
	}
	if _, exists := s.users[email]; exists {
		return fmt.Errorf("admin user already exists")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	s.users[email] = &UserEntry{
		Email:        email,
		PasswordHash: string(hash),
		Tokens:       nil,
		CreatedAt:    time.Now().Format(time.RFC3339),
		IsAdmin:      true,
	}

	if err := s.persistLocked(); err != nil {
		delete(s.users, email)
		return err
	}
	return nil
}

func (s *UserStore) IsAdmin(email string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[email]
	return ok && u.IsAdmin
}

func (s *UserStore) ListUsers() []*UserEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*UserEntry, 0, len(s.users))
	for _, u := range s.users {
		result = append(result, u)
	}
	return result
}

func (s *UserStore) DeleteUser(email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[email]
	if !ok {
		return fmt.Errorf("user not found")
	}
	if u.IsAdmin {
		return fmt.Errorf("cannot delete admin user")
	}

	delete(s.users, email)
	if err := s.persistLocked(); err != nil {
		s.users[email] = u
		return err
	}
	return nil
}

func (s *UserStore) HasUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users) > 0
}

func (s *UserStore) persistLocked() error {
	if s.filePath == "" {
		return nil
	}
	uf := userFile{Users: s.users}
	data, err := yaml.Marshal(&uf)
	if err != nil {
		return fmt.Errorf("failed to marshal users: %w", err)
	}
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write user file: %w", err)
	}
	return nil
}
