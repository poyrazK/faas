-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS support_staff (
 account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 role text NOT NULL CHECK(role IN ('agent','manager','revoked')),
 profile jsonb NOT NULL CHECK(jsonb_typeof(profile)='object' AND octet_length(profile::text)<=16384)
);
CREATE TABLE IF NOT EXISTS support_resources (
 id uuid PRIMARY KEY,
 kind text NOT NULL CHECK(kind IN ('policy','team')),
 revision bigint NOT NULL CHECK(revision>0),
 data jsonb NOT NULL CHECK(jsonb_typeof(data)='object' AND octet_length(data::text)<=65536),
 updated_at timestamptz NOT NULL CHECK(isfinite(updated_at))
);
CREATE TABLE IF NOT EXISTS support_tickets (
 id uuid PRIMARY KEY,
 scope_key text NOT NULL CHECK(octet_length(scope_key)<=64),
 account_id uuid REFERENCES accounts(id) ON DELETE CASCADE,
 org_id uuid REFERENCES orgs(id) ON DELETE CASCADE,
 created_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 subject text NOT NULL CHECK(octet_length(subject) BETWEEN 1 AND 200),
 category text NOT NULL CHECK(category IN ('deployment','billing','account','incident','other')),
 status text NOT NULL CHECK(status IN ('open','pending','resolved')),
 revision bigint NOT NULL CHECK(revision>0),
 priority text NOT NULL CHECK(priority IN ('low','normal','high','urgent')),
 team_id uuid REFERENCES support_resources(id),
 assignee_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 state jsonb NOT NULL CHECK(jsonb_typeof(state)='object' AND octet_length(state::text)<=32768),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at)),
 updated_at timestamptz NOT NULL CHECK(isfinite(updated_at)),
 resolved_at timestamptz CHECK(resolved_at IS NULL OR isfinite(resolved_at)),
 CHECK((account_id IS NULL)<>(org_id IS NULL))
);
CREATE INDEX IF NOT EXISTS support_tickets_scope_idx ON support_tickets(scope_key,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_queue_idx ON support_tickets(team_id,status,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_assignee_idx ON support_tickets(assignee_id,status);
CREATE TABLE IF NOT EXISTS support_messages (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 author text NOT NULL CHECK(author IN ('customer','staff','note')),
 body text NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 10000),
 redacted boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL CHECK(isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS support_messages_ticket_idx ON support_messages(ticket_id,sequence);
CREATE INDEX IF NOT EXISTS support_messages_search_idx ON support_messages USING gin(to_tsvector('simple',body));
CREATE TABLE IF NOT EXISTS support_receipts (
 id uuid PRIMARY KEY,
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 ticket_id uuid REFERENCES support_tickets(id) ON DELETE CASCADE,
 operation text NOT NULL CHECK(octet_length(operation) BETWEEN 1 AND 64),
 resource_id uuid NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object' AND octet_length(result::text)<=8192),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at))
);
CREATE TABLE IF NOT EXISTS support_events (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 kind text NOT NULL CHECK(kind IN ('created','reply','note','resolved','reopened','paused','resumed','triage','handoff','redacted','engineering_saved','engineering_published')),
 data jsonb NOT NULL CHECK(jsonb_typeof(data)='object' AND octet_length(data::text)<=8192),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS support_events_ticket_idx ON support_events(ticket_id,sequence);
CREATE TABLE IF NOT EXISTS support_episodes (
 id uuid PRIMARY KEY,
 ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
 started_at timestamptz NOT NULL CHECK(isfinite(started_at)),
 data jsonb NOT NULL CHECK(jsonb_typeof(data)='object' AND octet_length(data::text)<=32768)
);
CREATE INDEX IF NOT EXISTS support_episodes_started_idx ON support_episodes(started_at,id);
CREATE TABLE IF NOT EXISTS support_previews (
 id uuid PRIMARY KEY,
 ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
 actor_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('redaction','engineering')),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=65536),
 expires_at timestamptz NOT NULL CHECK(isfinite(expires_at))
);
CREATE INDEX IF NOT EXISTS support_previews_expiry_idx ON support_previews(expires_at);
CREATE TABLE IF NOT EXISTS support_engineering (
 ticket_id uuid PRIMARY KEY REFERENCES support_tickets(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 data jsonb NOT NULL CHECK(jsonb_typeof(data)='object' AND octet_length(data::text)<=65536)
);
CREATE TABLE IF NOT EXISTS support_satisfaction (
 episode_id uuid PRIMARY KEY REFERENCES support_episodes(id) ON DELETE CASCADE,
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 rating integer NOT NULL CHECK(rating BETWEEN 1 AND 5),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at))
);
CREATE TABLE IF NOT EXISTS support_outbox (
 id uuid PRIMARY KEY REFERENCES support_events(id) ON DELETE CASCADE,
 ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
 state text NOT NULL CHECK(state IN ('queued','inflight','accepted','failed','skipped')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 lease_id uuid,
 available_at timestamptz NOT NULL CHECK(isfinite(available_at)),
 lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS support_outbox_ready_idx ON support_outbox(available_at,id) WHERE state IN ('queued','inflight');

CREATE TABLE IF NOT EXISTS support_admin_events (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 kind text NOT NULL CHECK(kind IN ('policy','team','staff')),
 resource_id uuid NOT NULL,
 data jsonb NOT NULL CHECK(jsonb_typeof(data)='object' AND octet_length(data::text)<=65536),
 created_at timestamptz NOT NULL CHECK(isfinite(created_at))
);

-- +goose Down
DROP TABLE support_admin_events;
DROP TABLE support_outbox;
DROP TABLE support_satisfaction;
DROP TABLE support_engineering;
DROP TABLE support_previews;
DROP TABLE support_episodes;
DROP TABLE support_events;
DROP TABLE support_receipts;
DROP TABLE support_messages;
DROP TABLE support_tickets;
DROP TABLE support_resources;
DROP TABLE support_staff;
