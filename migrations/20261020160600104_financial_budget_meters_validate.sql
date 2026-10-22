-- filename: 20261020160600104_financial_budget_meters_validate.sql
-- migration-compat: expand

-- +goose Up
-- VALIDATE takes SHARE UPDATE EXCLUSIVE, which does not block writes.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261020160000: validate the wider policy check that 20261020160527103
-- added NOT VALID. Every existing row selects only compute and egress.
ALTER TABLE financial_budget_policies VALIDATE CONSTRAINT financial_budget_policies_spec_v2_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
