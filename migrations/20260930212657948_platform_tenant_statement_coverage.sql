-- +goose Up
-- +goose StatementBegin
ALTER TABLE platform_tenant_statements
  ADD COLUMN IF NOT EXISTS coverage jsonb;

-- Before compact invoice lines, each line was one exact usage minute. Its
-- source identity and billable units are sufficient to seed the private
-- coverage ledger; the extra pricing fields are ignored when decoded as
-- PlatformTenantStatementCoverage.
UPDATE platform_tenant_statements SET coverage = lines WHERE coverage IS NULL;

ALTER TABLE platform_tenant_statements
  ALTER COLUMN coverage SET NOT NULL;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'platform_tenant_statements_coverage_array'
      AND conrelid = 'platform_tenant_statements'::regclass
  ) THEN
    ALTER TABLE platform_tenant_statements
      ADD CONSTRAINT platform_tenant_statements_coverage_array
        CHECK (jsonb_typeof(coverage) = 'array');
  END IF;
END $$;

COMMENT ON COLUMN platform_tenant_statements.lines IS
  'Compact immutable invoice lines grouped by app, source, and effective price source.';
COMMENT ON COLUMN platform_tenant_statements.coverage IS
  'Private immutable minute-level billable-unit evidence used to calculate additive statement revisions.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE platform_tenant_statements
  DROP CONSTRAINT IF EXISTS platform_tenant_statements_coverage_array,
  DROP COLUMN IF EXISTS coverage;
COMMENT ON COLUMN platform_tenant_statements.lines IS NULL;
-- +goose StatementEnd
