-- filename: 20261004090600457_object_mutation_events.sql

-- +goose Up
-- +goose StatementBegin
-- Persist the public selector in the same transaction as confirmed tracked
-- completion and publication. Native version IDs remain in private references.
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS version_id text NOT NULL DEFAULT '';
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_completion_version_shape') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_completion_version_shape CHECK
      (version_id = '' OR version_id = 'null' OR
       version_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$');
 END IF;
END $$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_completion_version_outcome') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_completion_version_outcome CHECK
      (version_id = '' OR (write_phase = 'settled' AND status = 'completed'));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Downgrading must not discard committed version acknowledgments.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM object_upload_completions WHERE version_id <> '') THEN
        RAISE EXCEPTION 'cannot discard tracked completion version results';
    END IF;
END $$;
ALTER TABLE object_upload_completions
    DROP CONSTRAINT object_upload_completion_version_outcome,
    DROP CONSTRAINT object_upload_completion_version_shape,
    DROP COLUMN version_id;
-- +goose StatementEnd
