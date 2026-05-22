-- User-submitted feedback (rendered in the /admin dashboard).
CREATE TABLE IF NOT EXISTS feedback (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    message    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS feedback_created_at_idx ON feedback (created_at DESC);

-- Expo Push tokens registered by the mobile app. PK on token (not user) so a
-- handset that signs into a different account just rewrites the row instead
-- of creating a duplicate registration.
CREATE TABLE IF NOT EXISTS push_tokens (
    token      TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform   TEXT NOT NULL CHECK (platform IN ('ios','android')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS push_tokens_user_idx ON push_tokens (user_id);

-- Admin-issued broadcasts. delivered/failed are stamped once Expo Push
-- returns its ticket array; recipients is the snapshot count at send time.
CREATE TABLE IF NOT EXISTS broadcasts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    recipients  INT NOT NULL DEFAULT 0,
    delivered   INT NOT NULL DEFAULT 0,
    failed      INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS broadcasts_sent_at_idx ON broadcasts (sent_at DESC);
