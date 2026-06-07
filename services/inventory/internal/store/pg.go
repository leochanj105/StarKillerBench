package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres-backed durable Store. Holds serialize against the
// same stock row via SELECT ... FOR UPDATE inside a transaction, so N
// concurrent Holds for the last unit produce exactly one winner and the
// rest see ErrInsufficient — no oversell. The schema is applied externally
// (see migrations/0001_init.sql); the store does not run migrations.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore opens a connection pool to the database at dsn and verifies
// connectivity. The caller owns the returned store and must Close it.
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

func (p *PGStore) Close() {
	if p.pool != nil {
		p.pool.Close()
	}
}

func (p *PGStore) SetStock(ctx context.Context, hotel, room, date string, quantity int32) error {
	// New total overrides any prior total; sold and held are preserved by
	// updating only the total column on conflict.
	_, err := p.pool.Exec(ctx, `
		INSERT INTO stock (hotel_id, room_type, date, total, sold, held)
		VALUES ($1, $2, $3, $4, 0, 0)
		ON CONFLICT (hotel_id, room_type, date)
		DO UPDATE SET total = EXCLUDED.total`,
		hotel, room, date, quantity)
	return err
}

func (p *PGStore) Hold(ctx context.Context, hotel, room, date string, quantity int32) (string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	// Lock the stock row for the duration of the transaction so concurrent
	// Holds for the same key serialize and cannot oversell.
	var total, sold, held int32
	err = tx.QueryRow(ctx, `
		SELECT total, sold, held FROM stock
		WHERE hotel_id = $1 AND room_type = $2 AND date = $3
		FOR UPDATE`,
		hotel, room, date).Scan(&total, &sold, &held)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if total-sold-held < quantity {
		return "", ErrInsufficient
	}

	id := newHoldID()
	if _, err := tx.Exec(ctx, `
		UPDATE stock SET held = held + $4
		WHERE hotel_id = $1 AND room_type = $2 AND date = $3`,
		hotel, room, date, quantity); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO holds (hold_id, hotel_id, room_type, date, quantity, status)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id, hotel, room, date, quantity, statusHeld); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (p *PGStore) Commit(ctx context.Context, holdID string) error {
	return p.consume(ctx, holdID, statusCommitted)
}

func (p *PGStore) Release(ctx context.Context, holdID string) error {
	return p.consume(ctx, holdID, statusReleased)
}

// ReturnStock decrements sold for the key by quantity, returning sold units
// to availability. The stock row is locked FOR UPDATE so the read-check-write
// against sold serializes with concurrent operations on the same key.
func (p *PGStore) ReturnStock(ctx context.Context, hotel, room, date string, quantity int32) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var sold int32
	err = tx.QueryRow(ctx, `
		SELECT sold FROM stock
		WHERE hotel_id = $1 AND room_type = $2 AND date = $3
		FOR UPDATE`,
		hotel, room, date).Scan(&sold)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if quantity > sold {
		return ErrExceedsSold
	}

	if _, err := tx.Exec(ctx, `
		UPDATE stock SET sold = sold - $4
		WHERE hotel_id = $1 AND room_type = $2 AND date = $3`,
		hotel, room, date, quantity); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// consume transitions a held hold to a terminal status. For committed, the
// quantity moves from held to sold; for released, it returns to available
// (held is decremented). The hold row is locked FOR UPDATE so two
// concurrent consumers of the same hold can't both win.
func (p *PGStore) consume(ctx context.Context, holdID, target string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var hotel, room, date, st string
	var qty int32
	err = tx.QueryRow(ctx, `
		SELECT hotel_id, room_type, date, quantity, status FROM holds
		WHERE hold_id = $1
		FOR UPDATE`, holdID).Scan(&hotel, &room, &date, &qty, &st)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if st != statusHeld {
		return ErrConsumed
	}

	var stockSQL string
	if target == statusCommitted {
		stockSQL = `UPDATE stock SET held = held - $4, sold = sold + $4
			WHERE hotel_id = $1 AND room_type = $2 AND date = $3`
	} else {
		stockSQL = `UPDATE stock SET held = held - $4
			WHERE hotel_id = $1 AND room_type = $2 AND date = $3`
	}
	if _, err := tx.Exec(ctx, stockSQL, hotel, room, date, qty); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE holds SET status = $2 WHERE hold_id = $1`, holdID, target); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
