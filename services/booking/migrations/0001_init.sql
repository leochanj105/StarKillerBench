-- booking v2 schema. Applied externally to the Postgres instance pointed
-- at by PG_DSN. The service itself does not run migrations.

CREATE TABLE IF NOT EXISTS bookings (
    booking_id      TEXT    PRIMARY KEY,
    idempotency_key TEXT    NOT NULL UNIQUE,   -- dedups retried CreateBooking
    user_id         TEXT    NOT NULL,
    hotel_id        TEXT    NOT NULL,
    room_type       TEXT    NOT NULL,
    date            TEXT    NOT NULL,
    amount          BIGINT  NOT NULL CHECK (amount > 0),
    currency        TEXT    NOT NULL,
    auth_id         TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK (status IN ('confirmed'))
);

CREATE INDEX IF NOT EXISTS idx_bookings_user
    ON bookings (user_id);

-- The UNIQUE constraint on idempotency_key is what makes concurrent
-- CreateBooking calls with the same key race-safe: the first insert wins,
-- a later insert with the same key conflicts, and the handler returns the
-- already-stored booking_id without re-running the saga.
