-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-566. Account locks serialize intent, reservations and settlement. Records
-- deliberately retain expired funding and never depend on a process-local TTL.
CREATE TABLE IF NOT EXISTS financial_compute_books (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    period_start timestamptz NOT NULL,
    period_end timestamptz NOT NULL CHECK (period_end > period_start),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    CHECK (COALESCE((payload->>'committed')::bigint >= 0 AND (payload->>'reserved')::bigint >= 0
      AND (payload->>'committed')::bigint+(payload->>'reserved')::bigint <= 9007199254740991,false)),
    PRIMARY KEY(account_id, app_id, period_start)
);
CREATE TABLE IF NOT EXISTS financial_compute_leases (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    instance_id text NOT NULL CHECK (length(instance_id) BETWEEN 1 AND 128),
    node_id uuid NOT NULL,
    incarnation uuid NOT NULL,
    period_start timestamptz NOT NULL,
    strict boolean NOT NULL,
    started_at timestamptz NOT NULL,
    funded_until timestamptz NOT NULL CHECK (funded_until >= started_at),
    settled_at timestamptz CHECK (settled_at >= started_at),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object')
);
CREATE UNIQUE INDEX IF NOT EXISTS financial_compute_one_live_instance ON financial_compute_leases(instance_id) WHERE settled_at IS NULL;
CREATE INDEX IF NOT EXISTS financial_compute_app ON financial_compute_leases(account_id, app_id, period_start);
CREATE INDEX IF NOT EXISTS financial_compute_recovery ON financial_compute_leases(node_id, incarnation) WHERE settled_at IS NULL;
CREATE TABLE IF NOT EXISTS financial_compute_holds (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    policy_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    period_start timestamptz NOT NULL,
    PRIMARY KEY(account_id, policy_id, revision, period_start),
    FOREIGN KEY(account_id, policy_id, revision) REFERENCES financial_budget_revisions(account_id, policy_id, revision)
);
CREATE TABLE IF NOT EXISTS financial_compute_owners (
    node_id uuid PRIMARY KEY REFERENCES compute_nodes(id) ON DELETE CASCADE,
    incarnation uuid NOT NULL,
    protocol smallint NOT NULL CHECK (protocol = 1),
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE OR REPLACE FUNCTION financial_app_command_held(target_app uuid, target_deployment uuid, observed_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT financial_preview_command_held(target_app,target_deployment,observed_at) OR EXISTS(
 SELECT 1 FROM apps a JOIN financial_budget_policies p ON p.account_id=a.account_id
 JOIN financial_compute_holds h ON h.account_id=p.account_id AND h.policy_id=p.id AND h.revision=p.revision
 WHERE a.id=target_app AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
  AND p.spec->>'mode'='strict' AND p.spec->'scope'->>'kind'='app' AND p.spec->'scope'->>'id'=a.id::text
  AND (p.spec->>'resume_rule'='manual' OR h.period_start=date_trunc('month',observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'))
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION financial_app_command_held(uuid,uuid,timestamptz);
DROP TABLE financial_compute_owners;
DROP TABLE financial_compute_holds;
DROP TABLE financial_compute_leases;
DROP TABLE financial_compute_books;
