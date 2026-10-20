-- filename: 20261020160527103_financial_object_storage_estimates.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key and the new policy constraint lock their tables briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261020160000: apid's live object-storage estimate for each account
-- and UTC month: the cumulative usage, the pricing and plan allowance it was
-- priced with, and whether the usage was fresh. The cost report reads it as
-- the account-level object_storage meter, so budgets cover object storage.
-- apid is the only writer; the newest observation replaces the row.
CREATE TABLE IF NOT EXISTS financial_object_storage_estimates (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  observed_at timestamptz NOT NULL CHECK (observed_at >= period_start),
  usage_through timestamptz NOT NULL CHECK (usage_through >= period_start AND usage_through <= observed_at),
  fresh boolean NOT NULL,
  stored_byte_hours bigint NOT NULL CHECK (stored_byte_hours >= 0),
  request_count bigint NOT NULL CHECK (request_count >= 0),
  egress_bytes bigint NOT NULL CHECK (egress_bytes >= 0),
  currency text NOT NULL CHECK (currency = 'USD'),
  storage_millicents_per_gib_month bigint NOT NULL CHECK (storage_millicents_per_gib_month >= 0),
  requests_millicents_per_million bigint NOT NULL CHECK (requests_millicents_per_million >= 0),
  egress_millicents_per_gib bigint NOT NULL CHECK (egress_millicents_per_gib >= 0),
  included_byte_hours bigint NOT NULL CHECK (included_byte_hours >= 0),
  included_requests bigint NOT NULL CHECK (included_requests >= 0),
  included_egress_bytes bigint NOT NULL CHECK (included_egress_bytes >= 0),
  PRIMARY KEY (account_id, period_start)
);

-- Budgets may now select object_storage (account scope only; enforced in
-- financial.ValidateBudget). The wider check replaces
-- financial_budget_policies_spec_check in three steps: added NOT VALID here,
-- validated by 20261020160600104, and the old check dropped by
-- 20261020160738991. The previous release writes only compute and egress,
-- which both checks accept.
ALTER TABLE financial_budget_policies ADD CONSTRAINT financial_budget_policies_spec_v2_check CHECK (
    jsonb_typeof(spec) = 'object'
    AND spec ?& ARRAY['name','scope','currency','meters','basis','limit_millicents','notify_millicents','mode','action','drain_seconds','resume_rule','enabled']
    AND jsonb_typeof(spec->'name') = 'string' AND length(spec->>'name') BETWEEN 1 AND 128
    AND jsonb_typeof(spec->'scope') = 'object' AND spec->'scope'->>'kind' IN ('account','project','environment','app','job')
    AND spec->>'currency' IN ('EUR', 'USD')
    AND jsonb_typeof(spec->'meters') = 'array' AND jsonb_array_length(spec->'meters') BETWEEN 1 AND 3
    AND spec->'meters' <@ '["compute","egress","object_storage"]'::jsonb
    AND spec->>'basis' IN ('net_usage','gross_usage')
    AND jsonb_typeof(spec->'limit_millicents') = 'number' AND (spec->>'limit_millicents')::bigint BETWEEN 0 AND 9007199254740991
    AND jsonb_typeof(spec->'notify_millicents') = 'array' AND jsonb_array_length(spec->'notify_millicents') <= 8
    AND spec->>'mode' IN ('monitored','strict')
    AND spec->>'action' IN ('notify','reject_traffic','suspend_background','stop_previews','suspend_workloads')
    AND jsonb_typeof(spec->'drain_seconds') = 'number' AND (spec->>'drain_seconds')::integer BETWEEN 0 AND 300
    AND spec->>'resume_rule' IN ('manual','next_period')
    AND jsonb_typeof(spec->'enabled') = 'boolean'
) NOT VALID;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE financial_budget_policies DROP CONSTRAINT IF EXISTS financial_budget_policies_spec_v2_check;
DROP TABLE IF EXISTS financial_object_storage_estimates;
-- +goose StatementEnd
