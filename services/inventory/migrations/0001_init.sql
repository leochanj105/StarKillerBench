-- inventory v2 schema. Applied externally to the Postgres instance
-- pointed at by PG_DSN. The service itself does not run migrations.

CREATE TABLE IF NOT EXISTS stock (
    hotel_id  TEXT    NOT NULL,
    room_type TEXT    NOT NULL,
    date      TEXT    NOT NULL,
    total     INTEGER NOT NULL CHECK (total >= 0),
    sold      INTEGER NOT NULL DEFAULT 0 CHECK (sold  >= 0),
    held      INTEGER NOT NULL DEFAULT 0 CHECK (held  >= 0),
    PRIMARY KEY (hotel_id, room_type, date)
);

CREATE TABLE IF NOT EXISTS holds (
    hold_id   TEXT    PRIMARY KEY,
    hotel_id  TEXT    NOT NULL,
    room_type TEXT    NOT NULL,
    date      TEXT    NOT NULL,
    quantity  INTEGER NOT NULL CHECK (quantity > 0),
    status    TEXT    NOT NULL CHECK (status IN ('held','committed','released'))
);

CREATE INDEX IF NOT EXISTS idx_holds_stock_key
    ON holds (hotel_id, room_type, date);
