CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name TEXT NOT NULL,
    avatar_url   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rooms (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code       TEXT NOT NULL,
    name       TEXT,
    owner_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at   TIMESTAMPTZ
);

-- only one active room can hold a given code at a time
CREATE UNIQUE INDEX IF NOT EXISTS rooms_code_active_uniq
    ON rooms (code) WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS room_members (
    room_id   UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','member')),
    muted     BOOLEAN NOT NULL DEFAULT FALSE,
    kicked    BOOLEAN NOT NULL DEFAULT FALSE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    left_at   TIMESTAMPTZ,
    PRIMARY KEY (room_id, user_id)
);

CREATE INDEX IF NOT EXISTS room_members_user_active_idx
    ON room_members (user_id) WHERE left_at IS NULL;
