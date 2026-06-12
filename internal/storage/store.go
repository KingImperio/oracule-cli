package storage

import (
	"context"
	"errors"
	"sync"

	"github.com/KingImperio/oracule-cli/pkg/models"
)

// ErrNotFound is returned when a session or message does not exist.
var ErrNotFound = errors.New("not found")

// MemoryStore is the in-memory implementation of the agent.SessionStore interface.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]models.Session
	messages map[string][]models.Message // keyed by message ID
}

// NewInMemory returns a session store that keeps everything in RAM.
func NewInMemory() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]models.Session),
		messages: make(map[string][]models.Message),
	}
}

// GetSession retrieves a session by ID.
func (s *MemoryStore) GetSession(_ context.Context, id string) (*models.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		return &sess, nil
	}
	return nil, ErrNotFound
}

// SaveSession creates or updates a session.
func (s *MemoryStore) SaveSession(_ context.Context, sess *models.Session) error {
	if sess == nil {
		return errors.New("session is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.ID] = *sess
	return nil
}

// AppendMessage stores a message under the given session.
func (s *MemoryStore) AppendMessage(_ context.Context, m models.Message) error {
	if m.ID == "" {
		return errors.New("message ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[m.ID] = append(s.messages[m.ID], m)
	return nil
}

// AppendPart appends a part to an existing message.
func (s *MemoryStore) AppendPart(_ context.Context, sessionID, messageID string, p models.Part) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.messages[messageID]
	for i := range msgs {
		if msgs[i].ID == messageID {
			msgs[i].Parts = append(msgs[i].Parts, p)
			s.messages[messageID] = msgs
			break
		}
	}
	return nil
}

// GetMessages retrieves recent messages for a session.
func (s *MemoryStore) GetMessages(_ context.Context, sessionID string, limit int) ([]models.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []models.Message
	for _, msgs := range s.messages {
		for _, m := range msgs {
			if m.SessionID == sessionID {
				result = append(result, m)
			}
		}
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result, nil
}

// CreateRevertPoint stores a revert point.
func (s *MemoryStore) CreateRevertPoint(_ context.Context, rp models.RevertPoint) error {
	return nil
}

// GetRevertPoint retrieves a revert point.
func (s *MemoryStore) GetRevertPoint(_ context.Context, sessionID, messageID string) (*models.RevertPoint, error) {
	return nil, ErrNotFound
}
