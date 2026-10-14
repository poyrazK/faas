-- +goose Up
ALTER TABLE event_execution_lineage_cases ADD COLUMN IF NOT EXISTS diagnostic_id uuid CHECK(diagnostic_id IS NULL OR diagnostic_id<>'00000000-0000-0000-0000-000000000000'::uuid);

-- +goose Down
-- Refuse to discard incarnation guards while diagnostic cases or guarded work remain.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM event_execution_lineage_cases WHERE diagnostic_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM event_execution_lineage_retry_jobs j CROSS JOIN LATERAL jsonb_array_elements(j.snapshot->'items') i WHERE i->'selection'->>'expected_diagnostic_id' IS NOT NULL) THEN
  RAISE EXCEPTION 'prune guarded diagnostic cases and retry jobs before downgrading';
 END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE event_execution_lineage_cases DROP COLUMN diagnostic_id;
