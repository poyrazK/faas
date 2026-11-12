-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- destination_hash is a SHA-256 recipient-address binding, never a credential.
CREATE TABLE IF NOT EXISTS support_notifications (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 event_id uuid NOT NULL REFERENCES support_events(id) ON DELETE CASCADE,
 ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
 recipient_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 audience text NOT NULL CHECK(audience IN ('customer','staff')),
 kind text NOT NULL CHECK(kind IN ('staff_reply','customer_reply','ticket_created','assigned')),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at)),
 read_at timestamptz CHECK(read_at IS NULL OR isfinite(read_at)),
 email_state text NOT NULL CHECK(email_state IN ('queued','inflight','accepted','failed','skipped')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 lease_id uuid,
 lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
 available_at timestamptz NOT NULL CHECK(isfinite(available_at)),
 destination_hash bytea CHECK(destination_hash IS NULL OR octet_length(destination_hash)=32),
 UNIQUE(event_id,recipient_id,audience)
);
CREATE INDEX IF NOT EXISTS support_notifications_recipient_idx ON support_notifications(recipient_id,audience,ticket_id,sequence);
CREATE INDEX IF NOT EXISTS support_notifications_unread_idx ON support_notifications(recipient_id,audience,sequence) WHERE read_at IS NULL;
CREATE INDEX IF NOT EXISTS support_notifications_ready_idx ON support_notifications(available_at,id) WHERE email_state IN ('queued','inflight');
CREATE INDEX IF NOT EXISTS support_notifications_age_idx ON support_notifications(created_at,id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS support_notifications;
