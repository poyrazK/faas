-- +goose Up
-- +goose StatementBegin
DO $replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='workflow_recovery_history_events'::regclass AND conname='workflow_recovery_history_initial_lifecycle_check') THEN RETURN; END IF;
 ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_events_check;
 ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_events_check CHECK((
  jsonb_typeof(entry)='object' AND entry->'durable'='true'::jsonb
  AND entry->>'id'='audit:'||id::text
  AND entry->>'kind' IN ('candidates_observed','policy_changed','policy_paused','retry_transition','health_opened','health_changed','health_closed','health_alert_captured','health_recovered_captured','retry_parent_evidence_repaired','retry_child_evidence_repaired','retry_evidence_blocked','retry_evidence_cleared','initial_evidence_repaired','initial_recovery_blocked','initial_recovery_cleared','initial_recovery_prepared','initial_recovery_submitted','initial_recovery_cancelled','initial_recovery_expired','initial_recovery_stale','initial_recovery_result')
  AND jsonb_typeof(entry->'reason')='string' AND jsonb_typeof(entry->'outcome')='string'
  AND (entry->>'policy_version')::bigint>0
  AND (entry->>'occurred_at')::timestamptz=occurred_at
  AND (entry->>'kind'<>'policy_changed' OR jsonb_typeof(entry->'policy')='object')
 ) IS TRUE);
 ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_initial_lifecycle_check CHECK ((
 entry->>'kind' NOT IN ('initial_recovery_prepared','initial_recovery_submitted','initial_recovery_cancelled','initial_recovery_expired','initial_recovery_stale','initial_recovery_result') OR (
 entry->>'evidence_kind'='initial' AND (entry->>'resume_count')::integer=1
 AND entry->>'run_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND entry->>'initial_failure_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND entry->>'review_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND entry->>'action_id'=entry->>'review_id'
 AND jsonb_typeof(entry->'actor_kind')='string' AND length(entry->>'actor_kind')>0
 AND jsonb_typeof(entry->'actor_id')='string' AND length(entry->>'actor_id')>0
 AND (entry->>'kind'<>'initial_recovery_result' OR entry->>'outcome' IN ('succeeded','failed','cancelled'))
 )) IS TRUE);
END $replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_history_events WHERE entry->>'kind' IN ('initial_recovery_prepared','initial_recovery_submitted','initial_recovery_cancelled','initial_recovery_expired','initial_recovery_stale','initial_recovery_result')) THEN RAISE EXCEPTION 'cannot downgrade while initial lifecycle history is retained'; END IF;
 ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_initial_lifecycle_check;
 ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_events_check;
 ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_events_check CHECK((
  jsonb_typeof(entry)='object' AND entry->'durable'='true'::jsonb
  AND entry->>'id'='audit:'||id::text
  AND entry->>'kind' IN ('candidates_observed','policy_changed','policy_paused','retry_transition','health_opened','health_changed','health_closed','health_alert_captured','health_recovered_captured','retry_parent_evidence_repaired','retry_child_evidence_repaired','retry_evidence_blocked','retry_evidence_cleared','initial_evidence_repaired','initial_recovery_blocked','initial_recovery_cleared')
  AND jsonb_typeof(entry->'reason')='string' AND jsonb_typeof(entry->'outcome')='string'
  AND (entry->>'policy_version')::bigint>0
  AND (entry->>'occurred_at')::timestamptz=occurred_at
  AND (entry->>'kind'<>'policy_changed' OR jsonb_typeof(entry->'policy')='object')
 ) IS TRUE);
END $$;
-- +goose StatementEnd
