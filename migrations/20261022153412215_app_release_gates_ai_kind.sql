-- filename: 20261022153412215_app_release_gates_ai_kind.sql
-- migration-compat: expand
--
-- ADR 20261022153155: release gates of kind ai compare a release's AI
-- calls with its baseline's. They reuse metric_name (error_rate,
-- flagged_rate, cost_per_call or ttfb_ms) and threshold (the largest
-- increase in percent, 1-1000); comparison stays NULL.
--
-- The kind CHECK is replaced online and keeps its name, as
-- 20261021085211109 replaced the edge-rule outcome CHECK: the wider list is
-- added NOT VALID under a temporary name and validated, the old one dropped,
-- the wider list added back under the old name and validated, and the
-- temporary one dropped. The ai gate's CHECK is added NOT VALID and
-- validated. Each step is its own transaction; a VALIDATE takes SHARE UPDATE
-- EXCLUSIVE, so gates are still read and written during the scans, and a
-- kind CHECK always holds. Every step is re-runnable
-- (TestNewMigrationsAreReplaySafe).
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $ai_gates$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_kind_check_v2') THEN
    ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_kind_check_v2
      CHECK (kind IN ('slo', 'new_errors', 'custom_metric', 'ai')) NOT VALID;
  END IF;
END
$ai_gates$;
-- +goose StatementEnd

ALTER TABLE app_release_gates VALIDATE CONSTRAINT app_release_gates_kind_check_v2;

ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_kind_check;

-- +goose StatementBegin
DO $ai_gates$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_kind_check') THEN
    ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_kind_check
      CHECK (kind IN ('slo', 'new_errors', 'custom_metric', 'ai')) NOT VALID;
  END IF;
END
$ai_gates$;
-- +goose StatementEnd

ALTER TABLE app_release_gates VALIDATE CONSTRAINT app_release_gates_kind_check;

ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_kind_check_v2;

-- +goose StatementBegin
DO $ai_gates$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_ai_check') THEN
    ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_ai_check
      CHECK (kind <> 'ai' OR (metric_name IN ('error_rate', 'flagged_rate', 'cost_per_call', 'ttfb_ms')
        AND threshold IS NOT NULL AND threshold BETWEEN 1 AND 1000
        AND comparison IS NULL AND slo_id IS NULL AND max_burn_rate IS NULL
        AND max_new_groups IS NULL AND min_events IS NULL)) NOT VALID;
  END IF;
END
$ai_gates$;
-- +goose StatementEnd

ALTER TABLE app_release_gates VALIDATE CONSTRAINT app_release_gates_ai_check;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DELETE FROM app_release_gates WHERE kind = 'ai';

-- +goose StatementBegin
DO $ai_gates$
BEGIN
  ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_ai_check;
  ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_kind_check_v2;
  ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_kind_check;
  ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_kind_check
    CHECK (kind IN ('slo', 'new_errors', 'custom_metric'));
END
$ai_gates$;
-- +goose StatementEnd

RESET lock_timeout;
