-- Per-user private "personal marker": a pin only its owner sees. Persisted so
-- it survives leaving/rejoining a room and signing in on another device. One
-- per (room, user); upserted in place. Never broadcast — other members never
-- see another member's personal marker.
CREATE TABLE IF NOT EXISTS personal_markers (
    room_id    UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    label      TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);
