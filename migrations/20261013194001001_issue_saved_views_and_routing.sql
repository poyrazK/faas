-- +goose Up
CREATE TABLE IF NOT EXISTS app_issue_saved_views (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 view_key text NOT NULL CHECK(view_key ~ '^[a-z0-9][a-z0-9-]{0,47}$'),
 name text NOT NULL CHECK(octet_length(name) BETWEEN 1 AND 64),
 filters jsonb NOT NULL CHECK(jsonb_typeof(filters)='object' AND octet_length(filters::text)<=2048),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 updated_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 PRIMARY KEY(app_id,view_key)
);

ALTER TABLE app_issue_ownership_rules ADD COLUMN IF NOT EXISTS source_path_prefix text;
ALTER TABLE app_issue_ownership_rules ADD COLUMN IF NOT EXISTS tag_filter text;
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issue_ownership_rules'::regclass AND conname='app_issue_ownership_rules_check' AND position('source_path_prefix' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE app_issue_ownership_rules DROP CONSTRAINT IF EXISTS app_issue_ownership_rules_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issue_ownership_rules'::regclass AND conname='app_issue_ownership_rules_check') THEN
  ALTER TABLE app_issue_ownership_rules ADD CONSTRAINT app_issue_ownership_rules_check CHECK ((exception_type IS NOT NULL AND length(exception_type) BETWEEN 1 AND 256) OR source_kind IS NOT NULL OR route_prefix IS NOT NULL OR source_path_prefix IS NOT NULL OR tag_filter IS NOT NULL);
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issue_ownership_rules'::regclass AND conname='app_issue_ownership_rules_source_path_prefix_check') THEN
  ALTER TABLE app_issue_ownership_rules ADD CONSTRAINT app_issue_ownership_rules_source_path_prefix_check CHECK(source_path_prefix IS NULL OR octet_length(source_path_prefix) BETWEEN 1 AND 256);
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issue_ownership_rules'::regclass AND conname='app_issue_ownership_rules_tag_filter_check') THEN
  ALTER TABLE app_issue_ownership_rules ADD CONSTRAINT app_issue_ownership_rules_tag_filter_check CHECK(tag_filter IS NULL OR (strpos(tag_filter,'=')>0 AND split_part(tag_filter,'=',1) IN ('component','operation','feature_flag','feature_variant','dependency','region','job_type','workflow','state','error_class','release_channel','runtime') AND octet_length(substring(tag_filter FROM strpos(tag_filter,'=')+1))<=128));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM app_issue_saved_views) OR EXISTS(SELECT 1 FROM app_issue_ownership_rules WHERE source_path_prefix IS NOT NULL OR tag_filter IS NOT NULL) THEN
  RAISE EXCEPTION 'saved views or extended issue routing are in use; remove them explicitly before rollback';
 END IF;
END $$;
ALTER TABLE app_issue_ownership_rules DROP CONSTRAINT app_issue_ownership_rules_check,
 DROP COLUMN source_path_prefix,DROP COLUMN tag_filter,
 ADD CONSTRAINT app_issue_ownership_rules_check CHECK((exception_type IS NOT NULL AND length(exception_type) BETWEEN 1 AND 256) OR source_kind IS NOT NULL OR route_prefix IS NOT NULL);
DROP TABLE app_issue_saved_views;
-- +goose StatementEnd
