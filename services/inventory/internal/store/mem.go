package store

import (
	"context"
	"fmt"
	"sync"
)

type stockRecord struct {
	total int32
	sold  int32
	held  int32
}

type holdRecord struct {
	stockKey string
	quantity int32
	status   string
}

// memStore is the in-memory backend. A single mutex serializes all
// operations, which is sufficient to prevent oversell for the v1 contract.
type memStore struct {
	mu    sync.Mutex
	stock map[string]*stockRecord
	holds map[string]*holdRecord
}

// NewMemStore returns an empty in-memory Store.
func NewMemStore() Store {
	return &memStore{
		stock: make(map[string]*stockRecord),
		holds: make(map[string]*holdRecord),
	}
}

func memKey(hotel, room, date string) string {
	return fmt.Sprintf("%s|%s|%s", hotel, room, date)
}

func (m *memStore) SetStock(_ context.Context, hotel, room, date string, quantity int32) error {
	key := memKey(hotel, room, date)
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.stock[key]; ok {
		rec.total = quantity
	} else {
		m.stock[key] = &stockRecord{total: quantity}
	}
	return nil
}

func (m *memStore) Hold(_ context.Context, hotel, room, date string, quantity int32) (string, error) {
	key := memKey(hotel, room, date)
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.stock[key]
	if !ok {
		return "", ErrNotFound
	}
	if rec.total-rec.sold-rec.held < quantity {
		return "", ErrInsufficient
	}
	rec.held += quantity

	id := newHoldID()
	m.holds[id] = &holdRecord{stockKey: key, quantity: quantity, status: statusHeld}
	return id, nil
}

func (m *memStore) Commit(_ context.Context, holdID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	h, ok := m.holds[holdID]
	if !ok {
		return ErrNotFound
	}
	if h.status != statusHeld {
		return ErrConsumed
	}
	rec := m.stock[h.stockKey]
	rec.held -= h.quantity
	rec.sold += h.quantity
	h.status = statusCommitted
	return nil
}

func (m *memStore) Release(_ context.Context, holdID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	h, ok := m.holds[holdID]
	if !ok {
		return ErrNotFound
	}
	if h.status != statusHeld {
		return ErrConsumed
	}
	rec := m.stock[h.stockKey]
	rec.held -= h.quantity
	h.status = statusReleased
	return nil
}

func (m *memStore) ReturnStock(_ context.Context, hotel, room, date string, quantity int32) error {
	key := memKey(hotel, room, date)
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.stock[key]
	if !ok {
		return ErrNotFound
	}
	if quantity > rec.sold {
		return ErrExceedsSold
	}
	rec.sold -= quantity
	return nil
}

func (m *memStore) Close() {}
