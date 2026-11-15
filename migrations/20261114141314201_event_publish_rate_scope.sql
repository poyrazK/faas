-- filename: 20261114141314201_event_publish_rate_scope.sql
-- migration-compat: expand
-- Event-driven hunt #16: the per-account event publish rate
-- (api.EventPublishLimits, ADR-20261026140000) was kept per process, so each
-- apid replica and each vmmd node admitted the plan rate on its own. Every
-- publisher now also charges one shared counter per account in scope
-- event_publish of pg_ratelimit_counters.
-- The widened scope CHECK is added under a new name NOT VALID and validated
-- separately before the old one is dropped. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $scope$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'pg_ratelimit_counters'::regclass AND conname = 'pg_ratelimit_counters_scope_v2_check') THEN
    ALTER TABLE pg_ratelimit_counters ADD CONSTRAINT pg_ratelimit_counters_scope_v2_check
      CHECK (scope IN ('app', 'account', 'rule', 'preauth', 'event_publish')) NOT VALID;
  END IF;
END
$scope$;
-- +goose StatementEnd

ALTER TABLE pg_ratelimit_counters VALIDATE CONSTRAINT pg_ratelimit_counters_scope_v2_check;
ALTER TABLE pg_ratelimit_counters DROP CONSTRAINT IF EXISTS pg_ratelimit_counters_scope_check;

-- +goose Down
-- Counters in the new scope can still exist, so keep the widened constraint
-- on downgrade.
SELECT 1;
