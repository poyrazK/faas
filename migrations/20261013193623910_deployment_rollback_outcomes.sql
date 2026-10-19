-- migration-compat: expand
-- filename: 20261013193623910_deployment_rollback_outcomes.sql
--
-- ADR-1446 follow-up: verified rollback outcomes. A plain rollback (manual,
-- dashboard, or first-wake 5xx auto-rollback) was recorded when it was
-- requested, never when it took effect, so a rollback that never served
-- looked identical to one that did. Each plain rollback now writes one row
-- here; apid's outcome worker resolves it against the target deployment:
-- `complete` once the target serves (with the measured recovery time and the
-- completion audit id), `failed` when the target leaves activation without
-- serving or the outcome deadline passes. Checked rollbacks keep their own
-- deployment_rollback_operations ledger.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS deployment_rollbacks (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 target_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 from_deployment_id uuid REFERENCES deployments(id) ON DELETE SET NULL,
 trigger text NOT NULL CHECK (trigger IN ('manual','auto_5xx','alert')),
 status text NOT NULL CHECK (status IN ('preparing','complete','failed')),
 requested_at timestamptz NOT NULL,
 finished_at timestamptz,
 recovery_ms bigint CHECK (recovery_ms IS NULL OR recovery_ms >= 0),
 audit_id bigint,
 error_code text NOT NULL DEFAULT '' CHECK (octet_length(error_code) <= 64),
 error text NOT NULL DEFAULT '' CHECK (octet_length(error) <= 1024),
 CHECK ((status = 'preparing') = (finished_at IS NULL)),
 CHECK (status <> 'complete' OR (recovery_ms IS NOT NULL AND audit_id IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS deployment_rollbacks_preparing_target
 ON deployment_rollbacks(target_deployment_id) WHERE status = 'preparing';
CREATE INDEX IF NOT EXISTS deployment_rollbacks_preparing
 ON deployment_rollbacks(requested_at, id) WHERE status = 'preparing';
CREATE INDEX IF NOT EXISTS deployment_rollbacks_app
 ON deployment_rollbacks(app_id, requested_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS deployment_rollbacks;
-- +goose StatementEnd
