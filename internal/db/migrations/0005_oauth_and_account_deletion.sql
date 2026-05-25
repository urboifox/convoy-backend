-- Identity & account-lifecycle changes for OAuth and Play-Store-compliant
-- account deletion. Backwards compatible with the existing guest-user flow:
-- guest users simply have no row in user_identities and an empty entitlements
-- object.

-- Optional contact / display fields populated by OAuth providers. We don't
-- store any provider-specific tokens here — only the surfaced identity bits.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS email        TEXT,
    ADD COLUMN IF NOT EXISTS entitlements JSONB NOT NULL DEFAULT '{}'::JSONB,
    -- Soft delete: stamped on `DELETE /account`. The auth middleware refuses
    -- requests for users with this set, but the row stays around (e.g. for
    -- 30 days) so feedback / broadcasts / etc don't immediately fan out
    -- foreign-key cascades on a regret.
    ADD COLUMN IF NOT EXISTS deleted_at   TIMESTAMPTZ;

-- One row per (user, provider) pair. `subject` is the provider's stable user
-- id (Google `sub`, Apple `sub`). The unique index lets us upsert in a single
-- statement on subsequent sign-ins.
CREATE TABLE IF NOT EXISTS user_identities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider     TEXT NOT NULL CHECK (provider IN ('google','apple','guest')),
    subject      TEXT NOT NULL,
    email        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);
CREATE INDEX IF NOT EXISTS user_identities_user_idx ON user_identities (user_id);

-- Tiny key-value store for runtime-tunable knobs that don't deserve a full
-- table. Currently used for `min_client_version` (so the admin can ratchet
-- the floor without a redeploy), but designed to grow as more feature flags
-- land.
CREATE TABLE IF NOT EXISTS app_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed an empty min-client-version row so /config can read it without any
-- conditional null-handling on the read path. "0.0.0" means "no floor".
INSERT INTO app_config (key, value) VALUES ('min_client_version', '0.0.0')
    ON CONFLICT (key) DO NOTHING;
