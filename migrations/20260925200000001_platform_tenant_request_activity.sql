-- +goose Up
-- +goose StatementBegin

-- Preserve the verified customer identity on the sampled request-debugger
-- row itself. Historical rows stay NULL; current resource links must never be
-- used to infer their customer later.
ALTER TABLE request_telemetry
  ADD COLUMN IF NOT EXISTS platform_tenant_id uuid;

CREATE INDEX IF NOT EXISTS request_telemetry_platform_tenant_received_idx
  ON request_telemetry (account_id, platform_tenant_id, received_at DESC, id DESC)
  WHERE platform_tenant_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS request_telemetry_platform_tenant_received_idx;
ALTER TABLE request_telemetry
  DROP COLUMN IF EXISTS platform_tenant_id;
-- +goose StatementEnd
