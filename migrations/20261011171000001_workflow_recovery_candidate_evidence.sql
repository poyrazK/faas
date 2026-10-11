-- +goose Up
ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_events_check;
ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_events_check CHECK((
  jsonb_typeof(entry)='object' AND entry->'durable'='true'::jsonb
  AND entry->>'id'='audit:'||id::text
  AND entry->>'kind' IN ('candidates_observed','policy_changed','policy_paused','retry_transition','health_opened','health_changed','health_closed','health_alert_captured','health_recovered_captured')
  AND jsonb_typeof(entry->'reason')='string' AND jsonb_typeof(entry->'outcome')='string'
  AND (entry->>'policy_version')::bigint>0
  AND (entry->>'occurred_at')::timestamptz=occurred_at
  AND (entry->>'kind'<>'policy_changed' OR jsonb_typeof(entry->'policy')='object')
 ) IS TRUE);
ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_candidate_evidence_check CHECK((
 entry->>'kind'<>'candidates_observed' OR (
 jsonb_typeof(entry->'candidate_evidence')='object'
 AND jsonb_typeof(entry->'candidate_evidence'->'decisions')='array'
 AND jsonb_typeof(entry->'candidate_evidence'->'policy')='object'
 AND entry->'candidate_evidence'->>'app_id'=app_id::text
 AND entry->'candidate_evidence'->>'review_id'=entry->>'review_id'
 AND entry->'candidate_evidence'->>'job_id'=entry->>'job_id'
 AND entry->'candidate_evidence'->'policy_version'=entry->'policy_version'
 AND (entry->'candidate_evidence'->>'observed_at')::timestamptz=occurred_at
 AND NOT (entry->'candidate_evidence' ? 'plan')
 )) IS TRUE);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_history_events WHERE entry->>'kind'='candidates_observed') THEN RAISE EXCEPTION 'cannot downgrade while candidate evidence is retained'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_candidate_evidence_check;
ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_events_check;
ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_events_check CHECK((
  jsonb_typeof(entry)='object' AND entry->'durable'='true'::jsonb
  AND entry->>'id'='audit:'||id::text
  AND entry->>'kind' IN ('policy_changed','policy_paused','retry_transition','health_opened','health_changed','health_closed','health_alert_captured','health_recovered_captured')
  AND jsonb_typeof(entry->'reason')='string' AND jsonb_typeof(entry->'outcome')='string'
  AND (entry->>'policy_version')::bigint>0
  AND (entry->>'occurred_at')::timestamptz=occurred_at
  AND (entry->>'kind'<>'policy_changed' OR jsonb_typeof(entry->'policy')='object')
 ) IS TRUE);
