-- filename: 20260922164807594_mirror_rollup_counted.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-221. Stop legacy mirror rollup workers before applying this migration;
-- old binaries do not understand the receipt and must not run after cutover.
-- The receipt and baseline are installed atomically. Replaying this migration
-- must NOT rebuild summaries once retention has removed counted ledger rows.
DO $$
DECLARE
    complete_hour timestamptz :=
        (date_trunc('hour', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
        - interval '7 days' + interval '1 hour';
BEGIN
    LOCK TABLE mirror_invocation_results, mirror_invocation_summary IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = 'mirror_invocation_results'::regclass
          AND attname = 'rollup_counted' AND NOT attisdropped
    ) THEN
        RETURN;
    END IF;

    ALTER TABLE mirror_invocation_results
        ADD COLUMN rollup_counted boolean NOT NULL DEFAULT false;

    -- The old date_trunc used the session timezone. Remove misaligned recent
    -- buckets before rebuilding in UTC, or the old and repaired buckets would
    -- both remain. Only touch rules with retained evidence and complete hours;
    -- historical/partially-pruned buckets remain outside this repair.
    DELETE FROM mirror_invocation_summary AS summary
    WHERE summary.hour_bucket >= complete_hour
      AND summary.hour_bucket <> (
          date_trunc('hour', summary.hour_bucket AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
      )
      AND EXISTS (
          SELECT 1 FROM mirror_invocation_results AS result
          WHERE result.mirror_rule_id = summary.rule_id
      );

    -- Whole hours inside retention can be repaired from the retained ledger.
    -- Older hours may already have lost raw rows: never reduce their existing
    -- totals. Historical overcounts/holes with missing raw evidence cannot be
    -- reconstructed exactly; ADR-221 documents this cutover limitation.
    INSERT INTO mirror_invocation_summary (
        rule_id, app_id, hour_bucket, total_invocations,
        status_diff_count, schema_diff_count, body_diff_count, crash_count,
        cap_at_max_count, sum_latency_ms, rolled_up_at
    )
    SELECT mirror_rule_id, app_id,
        date_trunc('hour', completed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
        count(*), count(*) FILTER (WHERE status_diff),
        count(*) FILTER (WHERE schema_diff), count(*) FILTER (WHERE body_diff),
        count(*) FILTER (WHERE crashed), 0, coalesce(sum(latency_ms), 0), now()
    FROM mirror_invocation_results
    GROUP BY 1, 2, 3
    ON CONFLICT (rule_id, hour_bucket) DO UPDATE SET
        total_invocations = CASE WHEN EXCLUDED.hour_bucket >= complete_hour
            THEN EXCLUDED.total_invocations ELSE greatest(mirror_invocation_summary.total_invocations, EXCLUDED.total_invocations) END,
        status_diff_count = CASE WHEN EXCLUDED.hour_bucket >= complete_hour
            THEN EXCLUDED.status_diff_count ELSE greatest(mirror_invocation_summary.status_diff_count, EXCLUDED.status_diff_count) END,
        schema_diff_count = CASE WHEN EXCLUDED.hour_bucket >= complete_hour
            THEN EXCLUDED.schema_diff_count ELSE greatest(mirror_invocation_summary.schema_diff_count, EXCLUDED.schema_diff_count) END,
        body_diff_count = CASE WHEN EXCLUDED.hour_bucket >= complete_hour
            THEN EXCLUDED.body_diff_count ELSE greatest(mirror_invocation_summary.body_diff_count, EXCLUDED.body_diff_count) END,
        crash_count = CASE WHEN EXCLUDED.hour_bucket >= complete_hour
            THEN EXCLUDED.crash_count ELSE greatest(mirror_invocation_summary.crash_count, EXCLUDED.crash_count) END,
        sum_latency_ms = CASE WHEN EXCLUDED.hour_bucket >= complete_hour
            THEN EXCLUDED.sum_latency_ms ELSE greatest(mirror_invocation_summary.sum_latency_ms, EXCLUDED.sum_latency_ms) END,
        rolled_up_at = now();

    UPDATE mirror_invocation_results SET rollup_counted = true;
END $$;

CREATE INDEX IF NOT EXISTS mirror_invocation_results_uncounted_idx
    ON mirror_invocation_results (completed_at) WHERE NOT rollup_counted;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS mirror_invocation_results_uncounted_idx;
ALTER TABLE mirror_invocation_results DROP COLUMN IF EXISTS rollup_counted;
-- +goose StatementEnd
