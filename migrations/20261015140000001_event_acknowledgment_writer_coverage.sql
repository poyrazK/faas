-- +goose Up

CREATE TABLE IF NOT EXISTS event_acknowledgment_writers (
 session_id uuid PRIMARY KEY,
 role text NOT NULL CHECK (role IN ('apid','schedd')),
 slot text NOT NULL CHECK (slot ~ '^[A-Za-z0-9_.:-]{1,128}$'),
 capability_version integer NOT NULL CHECK (capability_version>=0),
 inventory jsonb NOT NULL CHECK (jsonb_typeof(inventory)='array' AND jsonb_array_length(inventory)<=128),
 inventory_revision text NOT NULL CHECK (inventory_revision='' OR inventory_revision ~ '^[0-9a-f]{64}$'),
 started_at timestamptz NOT NULL,
 seen_at timestamptz NOT NULL CHECK (seen_at>=started_at),
 continuous_since timestamptz NOT NULL CHECK (continuous_since>=started_at AND continuous_since<=seen_at),
 expires_at timestamptz NOT NULL CHECK (expires_at>seen_at)
);
CREATE INDEX IF NOT EXISTS event_acknowledgment_writers_expiry ON event_acknowledgment_writers(expires_at);
CREATE INDEX IF NOT EXISTS event_acknowledgment_writers_seen ON event_acknowledgment_writers(seen_at);
CREATE TABLE IF NOT EXISTS event_acknowledgment_recording_coverage (
 singleton boolean PRIMARY KEY CHECK(singleton),
 inventory_revision text NOT NULL CHECK (inventory_revision='' OR inventory_revision ~ '^[0-9a-f]{64}$'),
 continuous_since timestamptz,
 verified_through timestamptz,
 updated_at timestamptz NOT NULL,
 CHECK ((continuous_since IS NULL)=(verified_through IS NULL))
);

-- +goose Down
SELECT 1;
