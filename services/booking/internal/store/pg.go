package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres backend. The schema lives in
// migrations/0001_init.sql and is applied externally (compose init script in
// tests, a migration tool in production) — the service does not run
// migrations at startup.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore opens a connection pool to dsn and verifies connectivity.
func NewPGStore(ctx context.Context, dsn string) (*PGStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PGStore{pool: pool}, nil
}

func (s *PGStore) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *PGStore) ByIdempotencyKey(ctx context.Context, key string) (string, bool, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT booking_id FROM bookings WHERE idempotency_key = $1`, key,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// Create inserts b. The UNIQUE constraint on idempotency_key makes this
// race-safe: the winning insert persists the row; a losing concurrent insert
// conflicts on the key, ON CONFLICT DO NOTHING suppresses the error, and we
// resolve the already-stored booking_id and return created=false.
func (s *PGStore) Create(ctx context.Context, b Booking) (string, bool, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO bookings
			(booking_id, idempotency_key, user_id, hotel_id, room_type,
			 date, amount, currency, auth_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING booking_id`,
		b.BookingID, b.IdempotencyKey, b.UserID, b.HotelID, b.RoomType,
		b.Date, b.Amount, b.Currency, b.AuthID, b.Status,
	).Scan(&id)

	if errors.Is(err, pgx.ErrNoRows) {
		// Conflict: another caller already stored this key. Return theirs.
		existing, found, ferr := s.ByIdempotencyKey(ctx, b.IdempotencyKey)
		if ferr != nil {
			return "", false, ferr
		}
		if !found {
			return "", false, errors.New("insert conflict but no existing booking found")
		}
		return existing, false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (s *PGStore) Get(ctx context.Context, bookingID string) (Booking, bool, error) {
	var b Booking
	err := s.pool.QueryRow(ctx, `
		SELECT booking_id, idempotency_key, user_id, hotel_id, room_type,
		       date, amount, currency, auth_id, status
		FROM bookings WHERE booking_id = $1`, bookingID,
	).Scan(&b.BookingID, &b.IdempotencyKey, &b.UserID, &b.HotelID, &b.RoomType,
		&b.Date, &b.Amount, &b.Currency, &b.AuthID, &b.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, false, nil
	}
	if err != nil {
		return Booking{}, false, err
	}
	return b, true, nil
}

func (s *PGStore) ListByUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT booking_id FROM bookings WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Compile-time check that PGStore satisfies Store.
var _ Store = (*PGStore)(nil)
