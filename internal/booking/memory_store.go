package booking

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is a thread-safe in-memory BookingStore, intended for unit tests and local development.
// It replaces the old MemoryStore and the ConcurentStore.
type MemoryStore struct {
	mu       sync.Mutex
	seats    map[string]Booking // seatKey → booking
	sessions map[string]string  // sessionID → seatKey
	ttl      time.Duration
	now      func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		seats:    map[string]Booking{},
		sessions: map[string]string{},
		ttl:      defaultHoldTTL,
		now:      time.Now,
	}
}

func (s *MemoryStore) expired(b Booking) bool {
	return b.Status == StatusHeld && !s.now().Before(b.ExpiresAt)
}

// evictIfExpired must be called with s.mu held.
func (s *MemoryStore) evictIfExpired(key string) {
	if b, ok := s.seats[key]; ok && s.expired(b) {
		delete(s.seats, key)
		delete(s.sessions, b.ID)
	}
}

// lookup must be called with s.mu held.
func (s *MemoryStore) lookup(sessionID, userID string) (string, Booking, error) {
	key, ok := s.sessions[sessionID]
	if !ok {
		return "", Booking{}, ErrSessionNotFound
	}
	s.evictIfExpired(key)
	b, ok := s.seats[key]
	if !ok || b.ID != sessionID {
		return "", Booking{}, ErrSessionNotFound
	}
	if b.UserID != userID {
		return "", Booking{}, ErrForbidden
	}
	return key, b, nil
}

func (s *MemoryStore) Book(_ context.Context, b Booking) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := seatKey(b.MovieID, b.SeatID)
	s.evictIfExpired(key)
	if _, exists := s.seats[key]; exists {
		return Booking{}, ErrSeatAlreadyBooked
	}

	b.ID = uuid.New().String()
	b.Status = StatusHeld
	b.ExpiresAt = s.now().Add(s.ttl)
	s.seats[key] = b
	s.sessions[b.ID] = key
	return b, nil
}

func (s *MemoryStore) ListBookings(_ context.Context, movieID string) ([]Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []Booking
	for key, b := range s.seats {
		if s.expired(b) {
			delete(s.seats, key)
			delete(s.sessions, b.ID)
			continue
		}
		if b.MovieID == movieID {
			result = append(result, b)
		}
	}
	return result, nil
}

func (s *MemoryStore) Confirm(_ context.Context, sessionID, userID string) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, b, err := s.lookup(sessionID, userID)
	if err != nil {
		return Booking{}, err
	}
	b.Status = StatusConfirmed
	b.ExpiresAt = time.Time{}
	s.seats[key] = b
	return b, nil
}

func (s *MemoryStore) Release(_ context.Context, sessionID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, b, err := s.lookup(sessionID, userID)
	if err != nil {
		return err
	}
	if b.Status != StatusHeld {
		return ErrNotHeld
	}
	delete(s.seats, key)
	delete(s.sessions, sessionID)
	return nil
}
