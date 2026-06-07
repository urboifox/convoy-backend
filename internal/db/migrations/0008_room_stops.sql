-- Ordered, shared rest stops along a convoy's route. Owner-managed and visible
-- to everyone in the room. The client builds a multi-waypoint route that runs
-- origin -> stops (ascending position) -> destination, so the index orders by
-- (room_id, position) for a cheap ordered read.
CREATE TABLE IF NOT EXISTS room_stops (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    label      TEXT,
    position   INTEGER NOT NULL DEFAULT 0,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS room_stops_room_position_idx
    ON room_stops (room_id, position, created_at);
