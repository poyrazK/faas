-- filename: 20261031142449136_app_labels.sql

-- ADR-20261031100025: an app carries labels, a JSON object of Kubernetes-style
-- key/value pairs. Only apid writes the column; nothing on the request path
-- reads it. The constant default makes the column a metadata-only change and
-- an older apid ignores it.
-- migration-compat: expand
--
-- The object CHECK is added NOT VALID and validated in its own statement, so
-- apps keep being written during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE apps ADD COLUMN IF NOT EXISTS labels jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $labels$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'apps_labels_object_chk') THEN
    ALTER TABLE apps ADD CONSTRAINT apps_labels_object_chk
      CHECK (jsonb_typeof(labels) = 'object') NOT VALID;
  END IF;
END
$labels$;
-- +goose StatementEnd

ALTER TABLE apps VALIDATE CONSTRAINT apps_labels_object_chk;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_labels_object_chk;
ALTER TABLE apps DROP COLUMN IF EXISTS labels;
