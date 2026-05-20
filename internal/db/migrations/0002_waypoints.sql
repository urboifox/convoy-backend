CREATE TABLE IF NOT EXISTS waypoints (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    label      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS waypoints_room_idx ON waypoints (room_id);
