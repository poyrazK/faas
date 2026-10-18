-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='app_mcp_readiness_policy_history' AND column_name='recorded_at') THEN
  ALTER TABLE app_mcp_readiness_policy_history ADD COLUMN recorded_at timestamptz;
  UPDATE app_mcp_readiness_policy_history SET recorded_at=(policy->>'updated_at')::timestamptz;
  ALTER TABLE app_mcp_readiness_policy_history ALTER COLUMN recorded_at SET DEFAULT clock_timestamp(), ALTER COLUMN recorded_at SET NOT NULL;
  CREATE INDEX mcp_readiness_policy_history_page ON app_mcp_readiness_policy_history(app_id,scope,recorded_at DESC,revision DESC);
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
ALTER TABLE app_mcp_readiness_policy_history DROP COLUMN recorded_at;
