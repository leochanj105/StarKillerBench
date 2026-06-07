// Package store defines the persistence boundary for booking records.
//
// v2 keeps two interchangeable backends behind one Store interface:
//   - MemStore  — process memory; used by v1 unit tests and local dev when
//                 PG_DSN is unset.
//   - PGStore   — Postgres; durable and race-safe via a UNIQUE constraint on
//                 idempotency_key. Used in production and v2 unit tests.
package store

import (
	"context"
	"sync"
)

// Booking is the persisted record. status is always "confirmed" — failed
// sagas are never persisted. idempotency_key is unique across the store and
// is what dedups retried/concurrent CreateBooking calls.
type Booking struct {
	BookingID      string
	IdempotencyKey string
	UserID         string
	HotelID        string
	RoomType       string
	Date           string
	Amount         int64
	Currency       string
	AuthID         string
	Status         string
}

// Store is the persistence interface the booking server depends on. Both
// backends implement it; the server is agnostic to which one is wired in.
type Store interface {
	// ByIdempotencyKey returns the booking_id previously stored under key,
	// or found=false if none exists.
	ByIdempotencyKey(ctx context.Context, key string) (bookingID string, found bool, err error)

	// Create persists b. If a record with the same idempotency_key already
	// exists, it does NOT overwrite: it returns that record's booking_id with
	// created=false. Otherwise it stores b and returns (b.BookingID, true).
	// This is the durable dedup point for concurrent CreateBooking calls.
	Create(ctx context.Context, b Booking) (storedID string, created bool, err error)

	// Get returns the booking with the given id, or found=false.
	Get(ctx context.Context, bookingID string) (b Booking, found bool, err error)

	// ListByUser returns the booking_ids owned by userID (empty slice if none).
	ListByUser(ctx context.Context, userID string) ([]string, error)

	// Close releases backend resources. No-op for MemStore.
	Close()
}

// MemStore is the in-memory backend.
type MemStore struct {
	mu    sync.Mutex
	byID  map[string]Booking
	byKey map[string]string
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		byID:  make(map[string]Booking),
		byKey: make(map[string]string),
	}
}

func (s *MemStore) ByIdempotencyKey(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byKey[key]
	return id, ok, nil
}

func (s *MemStore) Create(_ context.Context, b Booking) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byKey[b.IdempotencyKey]; ok {
		return id, false, nil
	}
	s.byID[b.BookingID] = b
	s.byKey[b.IdempotencyKey] = b.BookingID
	return b.BookingID, true, nil
}

func (s *MemStore) Get(_ context.Context, bookingID string) (Booking, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[bookingID]
	return b, ok, nil
}

func (s *MemStore) ListByUser(_ context.Context, userID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for id, b := range s.byID {
		if b.UserID == userID {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *MemStore) Close() {}

// Compile-time check that MemStore satisfies Store.
var _ Store = (*MemStore)(nil)
