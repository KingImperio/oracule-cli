package storage

import (
	"context"
	"errors"
	"sync"
)

// Store is the minimal session persistence surface.
type Store interface {
	GetSession(ctx context.Context, id string) (*Session, error)
	SaveSession(ctx context.Context, s *Session) error
	AppendMessage(ctx context.Context, m Message) error
}

// Session and Message are thin contract types here to avoid importing
// pkg/models and creating a potential cycle.
type Session struct {
	ID        string
	Title     string
	Directory string
}

type Message struct {
	ID      string
	Role    string
	Content string
}

// ErrNotFound is returned when a session or message does not exist.
var ErrNotFound = errors.New("not found")

// MemoryStore is the in-memory implementation of Store.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	messages map[string][]Message
}

// NewInMemory returns a session store that keeps everything in RAM.
// Suitable for prototyping and for CLI sessions that don't need persistence.
func NewInMemory() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]Session),
		messages: make(map[string][]Message),
	}
}

// GetSession retrieves a session by ID.
func (s *MemoryStore) GetSession(_ context.Context, id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		return &sess, nil
	}
	return nil, ErrNotFound
}

// SaveSession creates or updates a session.
func (s *MemoryStore) SaveSession(_ context.Context, sess *Session) error {
	if sess == nil {
		return errors.New("session is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.ID] = *sess
	return nil
}

// AppendMessage stores a message under the given session ID keyed by its own ID.
func (s *MemoryStore) AppendMessage(_ context.Context, m Message) error {
	if m.ID == "" {
		return errors.New("message ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := m.ID
	s.messages[key] = append(s.messages[key], m)
	return nil
}

