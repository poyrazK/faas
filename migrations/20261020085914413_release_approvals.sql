-- migration-compat: expand
-- filename: 20261020085914413_release_approvals.sql
--
-- ADR-20261020085706: release approvals. An app's policy makes each
-- production release wait in a canary stage until enough eligible people
-- approve it; each person records one decision per release.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_release_approval_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 required_approvals smallint NOT NULL CHECK (required_approvals BETWEEN 1 AND 5),
 after_stage smallint NOT NULL CHECK (after_stage BETWEEN 1 AND 10),
 timeout_minutes integer NOT NULL CHECK (timeout_minutes BETWEEN 10 AND 10080),
 approvers text NOT NULL CHECK (approvers IN ('admin','developer')),
 allow_self_approval boolean NOT NULL,
 notify_channel_ids uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(notify_channel_ids) <= 5),
 updated_by text NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 320),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS deployment_release_approvals (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 approver_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 approver text NOT NULL CHECK (length(approver) BETWEEN 1 AND 320),
 approver_role text NOT NULL CHECK (approver_role IN ('owner','admin','developer','personal')),
 decision text NOT NULL CHECK (decision IN ('approve','reject')),
 comment text NOT NULL DEFAULT '' CHECK (length(comment) <= 200),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (deployment_id, approver_account_id)
);
CREATE INDEX IF NOT EXISTS deployment_release_approvals_app ON deployment_release_approvals(app_id, created_at);

-- One notification per release and event; meterd delivers the snapshot.
-- Scheduled releases (ADR-20261021152201) add the schedule_* events.
CREATE TABLE IF NOT EXISTS release_approval_events (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 event text NOT NULL CHECK (event IN ('requested','approved','rejected','expired','schedule_started','schedule_blocked')),
 channel_ids uuid[] NOT NULL CHECK (cardinality(channel_ids) BETWEEN 1 AND 5),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 8192),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','delivered','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 10),
 next_attempt_at timestamptz NOT NULL,
 last_error text NOT NULL DEFAULT '' CHECK (length(last_error) <= 512),
 created_at timestamptz NOT NULL DEFAULT now(),
 delivered_at timestamptz,
 UNIQUE (deployment_id, event)
);
CREATE INDEX IF NOT EXISTS release_approval_events_due ON release_approval_events(next_attempt_at) WHERE state = 'pending';
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP TABLE IF EXISTS release_approval_events;
DROP TABLE IF EXISTS deployment_release_approvals;
DROP TABLE IF EXISTS app_release_approval_policies;
-- +goose StatementEnd
