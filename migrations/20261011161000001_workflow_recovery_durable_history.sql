-- +goose Up
CREATE TABLE workflow_recovery_history_events (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE CHECK(sequence>0),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 occurred_at timestamptz NOT NULL CHECK(isfinite(occurred_at)),
 recorded_at timestamptz NOT NULL CHECK(isfinite(recorded_at)),
 entry jsonb NOT NULL CHECK((
  jsonb_typeof(entry)='object' AND entry->'durable'='true'::jsonb
  AND entry->>'id'='audit:'||id::text
  AND entry->>'kind' IN ('policy_changed','policy_paused','retry_transition','health_opened','health_changed','health_closed','health_alert_captured','health_recovered_captured')
  AND jsonb_typeof(entry->'reason')='string' AND jsonb_typeof(entry->'outcome')='string'
  AND (entry->>'policy_version')::bigint>0
  AND (entry->>'occurred_at')::timestamptz=occurred_at
  AND (entry->>'kind'<>'policy_changed' OR jsonb_typeof(entry->'policy')='object')
 ) IS TRUE)
);
CREATE INDEX workflow_recovery_history_events_app_time ON workflow_recovery_history_events(app_id,occurred_at DESC,id DESC);
CREATE INDEX workflow_recovery_history_events_app_recorded ON workflow_recovery_history_events(app_id,recorded_at DESC,id DESC);
CREATE INDEX workflow_recovery_history_events_recorded ON workflow_recovery_history_events(recorded_at,id);
CREATE INDEX workflow_recovery_history_events_app_sequence ON workflow_recovery_history_events(app_id,sequence DESC);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_history_events) THEN
  RAISE EXCEPTION 'cannot downgrade while durable recovery history is retained';
 END IF;
END $$;
DROP TABLE workflow_recovery_history_events;
-- +goose StatementEnd
