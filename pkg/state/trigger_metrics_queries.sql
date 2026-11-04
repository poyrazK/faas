-- name: CountTriggerRecordsByState :one
-- GET /v1/triggers/{id}/metrics (ADR-20261101104512): a trigger's records
-- per state, each count stopping at sqlc.arg(count_limit) so a trigger with
-- millions of settled rows stays an index range read on
-- (trigger_id, state). The handler used to count the newest 1,000 rows of
-- every state together.
SELECT
    (SELECT count(*) FROM (SELECT 1 FROM trigger_records
        WHERE trigger_id = sqlc.arg(trigger_id)::uuid AND state = 'pending' LIMIT sqlc.arg(count_limit)::bigint) p)::bigint AS pending,
    (SELECT count(*) FROM (SELECT 1 FROM trigger_records
        WHERE trigger_id = sqlc.arg(trigger_id)::uuid AND state = 'claimed' LIMIT sqlc.arg(count_limit)::bigint) c)::bigint AS claimed,
    (SELECT count(*) FROM (SELECT 1 FROM trigger_records
        WHERE trigger_id = sqlc.arg(trigger_id)::uuid AND state = 'succeeded' LIMIT sqlc.arg(count_limit)::bigint) s)::bigint AS succeeded,
    (SELECT count(*) FROM (SELECT 1 FROM trigger_records
        WHERE trigger_id = sqlc.arg(trigger_id)::uuid AND state = 'retry' LIMIT sqlc.arg(count_limit)::bigint) r)::bigint AS retry,
    (SELECT count(*) FROM (SELECT 1 FROM trigger_records
        WHERE trigger_id = sqlc.arg(trigger_id)::uuid AND state = 'dead_letter' LIMIT sqlc.arg(count_limit)::bigint) d)::bigint AS dead_letter;
