-- filename: 20261107164340118_request_analytics_lean_dimension_rows.sql
-- migration-compat: expand
-- ADR-20261103113952 amendment 3: rollup rows of the country, referrer_host,
-- ua_family and consumer_id kinds no longer split by HTTP status or
-- deployment, which no reader uses for those kinds. apid writes them with
-- status 0 and the nil deployment, so the status CHECK of both tiers now
-- allows 0 on non-route rows. Route rows keep a real status. The new CHECK
-- only widens what is allowed: the previous release writes rows it accepts,
-- and reads status only on route rows. It is added NOT VALID and validated
-- separately (SHARE UPDATE EXCLUSIVE), so apid keeps writing; the old CHECK
-- is dropped last. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $lean_status$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_analytics_minutes'::regclass AND conname = 'request_analytics_minutes_status_kind_chk') THEN
    ALTER TABLE request_analytics_minutes ADD CONSTRAINT request_analytics_minutes_status_kind_chk
      CHECK (status BETWEEN 100 AND 599 OR (status = 0 AND dimension_kind <> 'route')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_analytics_hours'::regclass AND conname = 'request_analytics_hours_status_kind_chk') THEN
    ALTER TABLE request_analytics_hours ADD CONSTRAINT request_analytics_hours_status_kind_chk
      CHECK (status BETWEEN 100 AND 599 OR (status = 0 AND dimension_kind <> 'route')) NOT VALID;
  END IF;
END
$lean_status$;
-- +goose StatementEnd

ALTER TABLE request_analytics_minutes VALIDATE CONSTRAINT request_analytics_minutes_status_kind_chk;
ALTER TABLE request_analytics_hours VALIDATE CONSTRAINT request_analytics_hours_status_kind_chk;

ALTER TABLE request_analytics_minutes DROP CONSTRAINT IF EXISTS request_analytics_minutes_status_check;
ALTER TABLE request_analytics_hours DROP CONSTRAINT IF EXISTS request_analytics_hours_status_check;

-- +goose Down
SET lock_timeout = '5s';
-- Rows written with status 0 would fail the old CHECK; drop them first.
DELETE FROM request_analytics_minutes WHERE status = 0;
DELETE FROM request_analytics_hours WHERE status = 0;
-- +goose StatementBegin
DO $lean_status_down$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_analytics_minutes'::regclass AND conname = 'request_analytics_minutes_status_check') THEN
    ALTER TABLE request_analytics_minutes ADD CONSTRAINT request_analytics_minutes_status_check CHECK (status BETWEEN 100 AND 599);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_analytics_hours'::regclass AND conname = 'request_analytics_hours_status_check') THEN
    ALTER TABLE request_analytics_hours ADD CONSTRAINT request_analytics_hours_status_check CHECK (status BETWEEN 100 AND 599);
  END IF;
END
$lean_status_down$;
-- +goose StatementEnd
ALTER TABLE request_analytics_minutes DROP CONSTRAINT IF EXISTS request_analytics_minutes_status_kind_chk;
ALTER TABLE request_analytics_hours DROP CONSTRAINT IF EXISTS request_analytics_hours_status_kind_chk;
