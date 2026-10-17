-- +goose Up
CREATE TABLE IF NOT EXISTS app_issue_lifecycle_alert_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision > 0),
 environments text[] NOT NULL CHECK(cardinality(environments)<=20 AND array_position(environments,NULL) IS NULL),
 cooldown_seconds bigint NOT NULL CHECK(cooldown_seconds BETWEEN 0 AND 604800)
);
CREATE TABLE IF NOT EXISTS issue_lifecycle_alert_receipts (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 last_alert_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,issue_id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM app_issue_lifecycle_alert_policies WHERE cardinality(environments)>0 OR cooldown_seconds>0) THEN
  RAISE EXCEPTION 'restore the default lifecycle alert policy before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE issue_lifecycle_alert_receipts;
DROP TABLE app_issue_lifecycle_alert_policies;
