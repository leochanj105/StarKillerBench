// Package store holds the inventory persistence layer behind a single
// Store interface. Two implementations exist:
//
//   - memStore: in-memory, per-process. Used by v1 unit tests and by local
//     dev when PG_DSN is unset.
//   - PGStore: Postgres-backed, durable, with row-level locking so
//     concurrent Holds for the same key serialize and never oversell.
//
// The server validates request shapes and maps the sentinel errors below
// to gRPC status codes; the store itself is gRPC-agnostic.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// Sentinel errors returned by Store implementations. The server maps these
// to gRPC codes (NotFound, FailedPrecondition).
var (
	// ErrNotFound: no stock record for the (hotel, room_type, date) key, or
	// no hold for the given hold_id.
	ErrNotFound = errors.New("not found")
	// ErrInsufficient: available stock (total - sold - held) is less than
	// the requested quantity.
	ErrInsufficient = errors.New("insufficient stock")
	// ErrConsumed: the hold has already been committed or released.
	ErrConsumed = errors.New("hold already consumed")
)

// Store is the persistence contract for inventory. Implementations must
// serialize concurrent Holds against the same key so that the available
// count never goes negative (no oversell).
type Store interface {
	// SetStock sets the total for one key, preserving sold and held.
	// quantity is assumed non-negative (validated by the caller).
	SetStock(ctx context.Context, hotel, room, date string, quantity int32) error
	// Hold reserves quantity units and returns an opaque hold_id.
	// quantity is assumed positive (validated by the caller).
	Hold(ctx context.Context, hotel, room, date string, quantity int32) (holdID string, err error)
	// Commit converts a held reservation to sold and consumes the hold.
	Commit(ctx context.Context, holdID string) error
	// Release returns a held reservation to available and consumes the hold.
	Release(ctx context.Context, holdID string) error
	// Close releases any resources held by the store.
	Close()
}

// Hold lifecycle statuses, persisted in the holds store.
const (
	statusHeld      = "held"
	statusCommitted = "committed"
	statusReleased  = "released"
)

// newHoldID returns an opaque, unguessable hold identifier.
func newHoldID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
