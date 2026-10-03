-- +goose Up
CREATE TABLE IF NOT EXISTS commit_sources (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
 enabled boolean NOT NULL DEFAULT true,
 sealed_connection bytea,
 credential_revision bigint NOT NULL DEFAULT 0,
 relay_status text NOT NULL DEFAULT 'unconfigured'
  CHECK(relay_status IN ('unconfigured','healthy','credential_unavailable',
   'database_unavailable','schema_unqualified','source_binding_unqualified',
   'replay_pending','retention_unqualified','cleanup_pending','blocked_scan_pending',
   'blocked_status_pending','handoff_pending','status_unavailable','blocked_events')),
 last_checked_at timestamptz,
 pending_events bigint,
 blocked_events bigint,
 oldest_pending_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(account_id,name),
 UNIQUE(account_id,id)
);
-- Receipt identities intentionally have no expiry and no invocation FK:
-- invocation retention must never reopen an already accepted source identity.
CREATE TABLE IF NOT EXISTS commit_receipts (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 source_id uuid NOT NULL,
 event_id uuid NOT NULL,
 event_type text NOT NULL,
 payload jsonb NOT NULL,
 invocation_id uuid NOT NULL,
 operation_state text NOT NULL DEFAULT 'accepted'
  CHECK(operation_state IN ('accepted','running','completed','cancelled','failed','unknown')),
 completed_at timestamptz,
 accepted_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,source_id) REFERENCES commit_sources(account_id,id) ON DELETE CASCADE,
 UNIQUE(account_id,source_id,event_id)
);
CREATE INDEX IF NOT EXISTS commit_receipts_source_history ON commit_receipts(account_id,source_id,accepted_at,id);
CREATE UNIQUE INDEX IF NOT EXISTS commit_receipts_operation_identity ON commit_receipts(invocation_id);

-- Completion facts follow the existing invocation ledger atomically. Minimal
-- operation history survives invocation payload/result retention.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_commit_operation_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE commit_receipts SET operation_state=CASE NEW.state
 WHEN 'pending' THEN 'accepted' WHEN 'dispatching' THEN 'running'
 WHEN 'completed' THEN 'completed' WHEN 'cancelled' THEN 'cancelled'
 WHEN 'failed' THEN 'failed' WHEN 'dead_letter' THEN 'failed'
 ELSE 'unknown' END, completed_at=NEW.completed_at
 WHERE invocation_id=NEW.id;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS commit_operation_state ON invocations;
CREATE TRIGGER commit_operation_state AFTER UPDATE OF state,completed_at ON invocations
 FOR EACH ROW EXECUTE FUNCTION record_commit_operation_state();

CREATE TABLE IF NOT EXISTS commit_blocked_events (
 account_id uuid NOT NULL,
 source_id uuid NOT NULL,
 event_id uuid NOT NULL,
 event_type text NOT NULL,
 blocked_code text NOT NULL,
 created_at timestamptz NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(source_id,event_id),
 FOREIGN KEY(account_id,source_id) REFERENCES commit_sources(account_id,id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS commit_replay_requests (
 account_id uuid NOT NULL,
 source_id uuid NOT NULL,
 event_id uuid NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed')),
 generation bigint NOT NULL DEFAULT 1,
 requested_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(source_id,event_id),
 FOREIGN KEY(account_id,source_id) REFERENCES commit_sources(account_id,id) ON DELETE CASCADE
);

-- +goose Down
DROP TRIGGER commit_operation_state ON invocations;
DROP FUNCTION record_commit_operation_state();
DROP TABLE commit_replay_requests;
DROP TABLE commit_blocked_events;
DROP TABLE commit_receipts;
DROP TABLE commit_sources;
