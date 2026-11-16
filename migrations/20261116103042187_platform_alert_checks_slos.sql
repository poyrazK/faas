-- filename: 20261116103042187_platform_alert_checks_slos.sql
-- migration-compat: expand
-- Default alerts for synthetic checks and SLOs (ADR-20261115194000,
-- amendment 2026-11-16): platform_alert_notices.kind gains 'check_failing'
-- and 'slo_burn'. The wider CHECK is added NOT VALID under a new name,
-- validated, then the old one dropped; re-runnable. Older meterd never
-- queues the new kinds and skips them when sending ("unknown notice kind").
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $widen$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_alert_notices'::regclass AND conname = 'platform_alert_notices_kind_v2_check') THEN
    ALTER TABLE platform_alert_notices ADD CONSTRAINT platform_alert_notices_kind_v2_check
      CHECK (kind IN ('app_outage', 'cron_failing_paused', 'check_failing', 'slo_burn')) NOT VALID;
  END IF;
END
$widen$;
-- +goose StatementEnd

ALTER TABLE platform_alert_notices VALIDATE CONSTRAINT platform_alert_notices_kind_v2_check;

ALTER TABLE platform_alert_notices DROP CONSTRAINT IF EXISTS platform_alert_notices_kind_check;

-- +goose Down
SET lock_timeout = '5s';

DELETE FROM platform_alert_notices WHERE kind IN ('check_failing', 'slo_burn');

-- +goose StatementBegin
DO $restore$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_alert_notices'::regclass AND conname = 'platform_alert_notices_kind_check') THEN
    ALTER TABLE platform_alert_notices ADD CONSTRAINT platform_alert_notices_kind_check
      CHECK (kind IN ('app_outage', 'cron_failing_paused'));
  END IF;
END
$restore$;
-- +goose StatementEnd

ALTER TABLE platform_alert_notices DROP CONSTRAINT IF EXISTS platform_alert_notices_kind_v2_check;
