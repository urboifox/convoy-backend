-- Destination metadata: a human-readable place name (from a tapped POI or
-- reverse geocoding) and an optional owner-authored note/description. Both are
-- nullable and additive, so existing rooms and older clients are unaffected.
ALTER TABLE rooms
    ADD COLUMN IF NOT EXISTS dest_name TEXT,
    ADD COLUMN IF NOT EXISTS dest_notes TEXT;
