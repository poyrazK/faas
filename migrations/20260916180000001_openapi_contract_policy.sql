-- +goose Up
-- Per-app production OpenAPI contract promotion policy. The observe default
-- preserves existing deployment behaviour while allowing customers to opt
-- into warning telemetry or explicit blocking after reviewing a diff.
-- +goose StatementBegin
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS openapi_contract_policy text NOT NULL DEFAULT 'observe';

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_openapi_contract_policy_chk;
ALTER TABLE apps
    ADD CONSTRAINT apps_openapi_contract_policy_chk
    CHECK (openapi_contract_policy IN ('observe', 'warn', 'block'));
-- +goose StatementEnd

-- +goose Down
-- Sentinel: policy state is part of the deployment safety contract and is
-- intentionally retained on automatic down migrations.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
