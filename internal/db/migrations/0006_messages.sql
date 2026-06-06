-- Persisted per-room text chat. Survives reconnects and is visible to anyone
-- who later joins the room. Paginated newest-first by the client via the
-- (created_at, id) cursor, so the composite index below is what keeps the
-- "latest page" + "older page" queries fast as the history grows.
CREATE TABLE IF NOT EXISTS messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS messages_room_created_idx
    ON messages (room_id, created_at DESC, id DESC);
