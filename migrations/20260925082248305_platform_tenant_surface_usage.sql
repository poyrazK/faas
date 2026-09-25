-- +goose Up
-- +goose StatementBegin
-- The source is immutable at request time. Anonymous app traffic stays in
-- the existing app ledger; only verified tenant-surface traffic enters the
-- cross-app tenant aggregate. Surface UUIDs are retained after unlinking.
ALTER TABLE api_consumer_usage_events
  ADD COLUMN IF NOT EXISTS platform_tenant_surface_id uuid;
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'api_consumer_usage_events'::regclass
      AND conname = 'api_consumer_usage_events_tenant_source_chk'
  ) THEN
    ALTER TABLE api_consumer_usage_events
      ADD CONSTRAINT api_consumer_usage_events_tenant_source_chk
      CHECK ((platform_tenant_surface_id IS NULL OR
              (platform_tenant_id IS NOT NULL AND consumer_key = '__anonymous__')) AND
             (platform_tenant_id IS NULL OR consumer_key <> '__anonymous__' OR
              platform_tenant_surface_id IS NOT NULL)) NOT VALID;
  END IF;
END $$;

ALTER TABLE platform_tenant_usage_minutes
  ADD COLUMN IF NOT EXISTS source_kind text NOT NULL DEFAULT 'consumer';
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'platform_tenant_usage_minutes'::regclass
      AND conname = 'platform_tenant_usage_minutes_source_kind_chk'
  ) THEN
    ALTER TABLE platform_tenant_usage_minutes
      ADD CONSTRAINT platform_tenant_usage_minutes_source_kind_chk
      CHECK (source_kind IN ('consumer', 'surface'));
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'platform_tenant_usage_minutes'::regclass
      AND contype = 'p'
      AND pg_get_constraintdef(oid) LIKE '%source_kind%'
  ) THEN
    ALTER TABLE platform_tenant_usage_minutes
      DROP CONSTRAINT IF EXISTS platform_tenant_usage_minutes_pkey;
    ALTER TABLE platform_tenant_usage_minutes
      ADD PRIMARY KEY (account_id, platform_tenant_id, app_id, source_kind, consumer_key, window_start);
  END IF;
END $$;

-- Keep historical participation even if a surface is later detached or
-- deleted. Handoff exclusion uses this snapshot, not today's binding.
CREATE TABLE IF NOT EXISTS platform_tenant_statement_surfaces (
  statement_id uuid NOT NULL REFERENCES platform_tenant_statements(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  surface_id uuid NOT NULL,
  PRIMARY KEY (statement_id, app_id, surface_id)
);
CREATE INDEX IF NOT EXISTS platform_tenant_statement_surfaces_lookup_idx
  ON platform_tenant_statement_surfaces (app_id, surface_id, statement_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_statement_surfaces;
DELETE FROM platform_tenant_usage_minutes WHERE source_kind = 'surface';
ALTER TABLE platform_tenant_usage_minutes DROP CONSTRAINT IF EXISTS platform_tenant_usage_minutes_pkey;
ALTER TABLE platform_tenant_usage_minutes
  ADD PRIMARY KEY (account_id, platform_tenant_id, app_id, consumer_key, window_start);
ALTER TABLE platform_tenant_usage_minutes
  DROP CONSTRAINT IF EXISTS platform_tenant_usage_minutes_source_kind_chk;
ALTER TABLE platform_tenant_usage_minutes DROP COLUMN IF EXISTS source_kind;
ALTER TABLE api_consumer_usage_events
  DROP CONSTRAINT IF EXISTS api_consumer_usage_events_tenant_source_chk;
ALTER TABLE api_consumer_usage_events DROP COLUMN IF EXISTS platform_tenant_surface_id;
-- +goose StatementEnd
