-- filename: 20261002203756148_financial_budget_policies.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-431: customer budget intent and immutable revision audit. Saving intent
-- is distinct from a meterd decision and scheduler/gateway acknowledgement.
CREATE TABLE IF NOT EXISTS financial_budget_policies (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  spec jsonb NOT NULL CHECK (
    jsonb_typeof(spec) = 'object'
    AND spec ?& ARRAY['name','scope','currency','meters','basis','limit_millicents','notify_millicents','mode','action','drain_seconds','resume_rule','enabled']
    AND jsonb_typeof(spec->'name') = 'string' AND length(spec->>'name') BETWEEN 1 AND 128
    AND jsonb_typeof(spec->'scope') = 'object' AND spec->'scope'->>'kind' IN ('account','project','environment','app','job')
    AND spec->>'currency' = 'EUR'
    AND jsonb_typeof(spec->'meters') = 'array' AND jsonb_array_length(spec->'meters') BETWEEN 1 AND 2
    AND spec->'meters' <@ '["compute","egress"]'::jsonb
    AND spec->>'basis' IN ('net_usage','gross_usage')
    AND jsonb_typeof(spec->'limit_millicents') = 'number' AND (spec->>'limit_millicents')::bigint BETWEEN 0 AND 9007199254740991
    AND jsonb_typeof(spec->'notify_millicents') = 'array' AND jsonb_array_length(spec->'notify_millicents') <= 8
    AND spec->>'mode' IN ('monitored','strict')
    AND spec->>'action' IN ('notify','reject_traffic','suspend_background','stop_previews','suspend_workloads')
    AND jsonb_typeof(spec->'drain_seconds') = 'number' AND (spec->>'drain_seconds')::integer BETWEEN 0 AND 300
    AND spec->>'resume_rule' IN ('manual','next_period')
    AND jsonb_typeof(spec->'enabled') = 'boolean'
  ),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (updated_at >= created_at),
  deleted_at timestamptz CHECK (deleted_at IS NULL OR deleted_at >= created_at),
  UNIQUE (account_id, id)
);
CREATE INDEX IF NOT EXISTS financial_budget_policies_account_idx ON financial_budget_policies(account_id, id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS financial_budget_revisions (
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  actor text NOT NULL CHECK (octet_length(actor) BETWEEN 1 AND 256),
  mutation text NOT NULL CHECK (mutation IN ('created','updated','deleted')),
  spec jsonb NOT NULL CHECK (jsonb_typeof(spec) = 'object'),
  recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (account_id, policy_id, revision),
  FOREIGN KEY (account_id, policy_id) REFERENCES financial_budget_policies(account_id, id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION notify_financial_budget_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('financial_budget_changed', NEW.account_id::text);
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS financial_budget_revision_notify ON financial_budget_revisions;
CREATE TRIGGER financial_budget_revision_notify AFTER INSERT ON financial_budget_revisions
FOR EACH ROW EXECUTE FUNCTION notify_financial_budget_revision();

CREATE OR REPLACE FUNCTION protect_financial_budget_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  -- Account removal may erase its financial data; ordinary policy deletion
  -- is a revisioned tombstone and never removes the audit trail.
  IF TG_OP = 'DELETE' AND NOT EXISTS(SELECT 1 FROM accounts WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'financial budget revisions are append-only';
END $$;
DROP TRIGGER IF EXISTS financial_budget_revision_immutable ON financial_budget_revisions;
CREATE TRIGGER financial_budget_revision_immutable BEFORE UPDATE OR DELETE ON financial_budget_revisions
FOR EACH ROW EXECUTE FUNCTION protect_financial_budget_revision();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS financial_budget_revisions;
DROP TABLE IF EXISTS financial_budget_policies;
DROP FUNCTION IF EXISTS protect_financial_budget_revision();
DROP FUNCTION IF EXISTS notify_financial_budget_revision();
-- +goose StatementEnd
