-- name: InsertDataRepairRehearsal :exec
INSERT INTO data_repair_rehearsals(id,operation_id,account_id,phase,record)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(operation_id)::uuid,sqlc.arg(account_id)::uuid,'queued',sqlc.arg(record)::jsonb);

-- name: GetDataRepairRehearsal :one
SELECT record FROM data_repair_rehearsals WHERE id=sqlc.arg(id)::uuid;

-- name: LockDataRepairRehearsal :one
SELECT record FROM data_repair_rehearsals WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: SaveDataRepairRehearsal :exec
UPDATE data_repair_rehearsals SET phase=sqlc.arg(phase)::text,node_id=sqlc.narg(node_id)::uuid,
 cancel_requested=sqlc.arg(cancel_requested)::boolean,deadline=sqlc.narg(deadline)::timestamptz,record=sqlc.arg(record)::jsonb
WHERE id=sqlc.arg(id)::uuid;

-- name: ListDataRepairRehearsals :many
SELECT record FROM data_repair_rehearsals
WHERE id>sqlc.arg(after_id)::uuid AND
 ((sqlc.arg(queued)::boolean AND phase='queued') OR
 (NOT sqlc.arg(queued)::boolean AND phase IN ('running','cleanup','publishing') AND (sqlc.narg(node_id)::uuid IS NULL OR node_id=sqlc.narg(node_id)::uuid)))
ORDER BY id LIMIT sqlc.arg(page_size)::int;

-- name: LockDataRepairRehearsalNode :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(node_id)::text, 161017));

-- name: CountDataRepairRehearsalNode :one
SELECT count(*) FROM data_repair_rehearsals WHERE node_id=sqlc.arg(node_id)::uuid AND phase IN ('running','cleanup');

-- name: DataRepairHealthSnapshot :one
WITH task_health AS (
 SELECT 't:' || t.id::text AS cursor_key,t.operation_id,t.id AS task_id,t.account_id,o.app_id,t.node_id,t.phase,
 COALESCE((t.record->>'phase_started_at')::timestamptz,t.created_at) AS phase_started_at,
 t.phase IN ('running','cleanup') AS capacity_held,t.cancel_requested,
 COALESCE(t.record->>'failure_code','') AS failure_code,
 CASE WHEN t.phase NOT IN ('running','cleanup') THEN 'unassigned'
 WHEN n.id IS NULL THEN 'missing'
 WHEN n.lifecycle='retired' THEN 'retired'
 WHEN n.lifecycle='unavailable' THEN 'unavailable'
 WHEN n.last_heartbeat_at<sqlc.arg(heartbeat_cutoff)::timestamptz OR n.last_heartbeat_at>sqlc.arg(observed_at)::timestamptz THEN 'heartbeat_stale'
 ELSE 'available' END AS node_status
 FROM data_repair_rehearsals t JOIN customer_operations o ON o.id=t.operation_id
 LEFT JOIN compute_nodes n ON n.id=t.node_id
 WHERE t.phase IN ('queued','running','cleanup','publishing')
), apply_health AS (
 SELECT 'o:' || o.id::text AS cursor_key,o.id AS operation_id,NULL::uuid AS task_id,o.account_id,o.app_id,NULL::uuid AS node_id,
 o.record->'data_repair'->>'phase' AS phase,
 COALESCE((o.record->'data_repair'->>'phase_started_at')::timestamptz,o.created_at) AS phase_started_at,
 false AS capacity_held,false AS cancel_requested,COALESCE(o.record->>'failure_code','') AS failure_code,'unassigned'::text AS node_status
 FROM customer_operations o
 WHERE o.data_repair_id IS NOT NULL AND
 ((o.state IN ('running','requires_reconciliation') AND o.record->'data_repair'->>'phase' IN ('applying','outcome_unknown','resolving'))
 OR o.record->'data_repair'->>'phase'='replacing')
), health AS (SELECT * FROM task_health UNION ALL SELECT * FROM apply_health),
phases AS (SELECT phase,count(*) AS count,min(phase_started_at) AS oldest_phase_started_at FROM health GROUP BY phase),
page AS (SELECT * FROM health WHERE cursor_key>sqlc.arg(after_key)::text ORDER BY cursor_key LIMIT sqlc.arg(page_size)::int)
SELECT jsonb_build_object(
 'phases',COALESCE((SELECT jsonb_agg(to_jsonb(phases) ORDER BY phase) FROM phases),'[]'::jsonb),
 'held_slots',(SELECT count(*) FROM health WHERE capacity_held),
 'unavailable_tasks',(SELECT count(*) FROM health WHERE capacity_held AND node_status<>'available'),
 'items',COALESCE((SELECT jsonb_agg(to_jsonb(page) ORDER BY cursor_key) FROM page),'[]'::jsonb)
) AS snapshot;
