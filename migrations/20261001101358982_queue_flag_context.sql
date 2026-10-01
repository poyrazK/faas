-- filename: 20261001101358982_queue_flag_context.sql

-- +goose Up
-- +goose StatementBegin
-- Flag-aware queue work retains verified customer admission identity just as
-- async invocations do. This lets tenant suspension stop undispatched queue
-- attempts and binds the propagated flag envelope to its customer.
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocation_platform_tenant_source;
ALTER TABLE invocations ADD CONSTRAINT invocation_platform_tenant_source CHECK (
  platform_tenant_id IS NULL OR source IN ('async_invoke', 'replay', 'queue')
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Remove queue-specific tenant bindings and contexts before restoring the
-- narrower source check. Queue payloads and their ordinary trace headers stay.
DROP TRIGGER IF EXISTS invocation_platform_tenant_guard ON invocations;
UPDATE invocations
   SET platform_tenant_id = NULL,
       headers = COALESCE(headers, '{}'::jsonb) - 'X-Faas-Flag-Context'
 WHERE source = 'queue' AND platform_tenant_id IS NOT NULL;
CREATE TRIGGER invocation_platform_tenant_guard BEFORE INSERT OR UPDATE ON invocations
  FOR EACH ROW EXECUTE FUNCTION guard_invocation_platform_tenant();
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocation_platform_tenant_source;
ALTER TABLE invocations ADD CONSTRAINT invocation_platform_tenant_source CHECK (
  platform_tenant_id IS NULL OR source IN ('async_invoke', 'replay')
);
-- +goose StatementEnd
