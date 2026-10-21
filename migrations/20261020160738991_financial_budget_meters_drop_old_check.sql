-- filename: 20261020160738991_financial_budget_meters_drop_old_check.sql
-- migration-compat: expand

-- +goose Up
-- Dropping a constraint takes a brief ACCESS EXCLUSIVE lock.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261020160000: the validated financial_budget_policies_spec_v2_check
-- now guards every column the old check did, and also accepts the
-- object_storage meter. Drop the narrower check so budgets can select it.
ALTER TABLE financial_budget_policies DROP CONSTRAINT IF EXISTS financial_budget_policies_spec_check;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Policies that selected object_storage must be removed by the operator
-- before the narrower check can be validated again.
ALTER TABLE financial_budget_policies ADD CONSTRAINT financial_budget_policies_spec_check CHECK (
    jsonb_typeof(spec) = 'object'
    AND spec ?& ARRAY['name','scope','currency','meters','basis','limit_millicents','notify_millicents','mode','action','drain_seconds','resume_rule','enabled']
    AND jsonb_typeof(spec->'name') = 'string' AND length(spec->>'name') BETWEEN 1 AND 128
    AND jsonb_typeof(spec->'scope') = 'object' AND spec->'scope'->>'kind' IN ('account','project','environment','app','job')
    AND spec->>'currency' IN ('EUR', 'USD')
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
) NOT VALID;
-- +goose StatementEnd
