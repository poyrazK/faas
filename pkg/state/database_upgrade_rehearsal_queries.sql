-- name: QueueDatabaseUpgradeRehearsal :exec
WITH queued AS (
    INSERT INTO database_restore_drills (drill_id, source_id, node_id, target_at, result)
    VALUES (sqlc.arg(drill_id), sqlc.arg(source_id), sqlc.arg(node_id), sqlc.arg(target_at), 'queued')
    ON CONFLICT DO NOTHING
    RETURNING drill_id
)
INSERT INTO database_upgrade_rehearsals (drill_id, request_id, source_major, target_major, database_name)
SELECT drill_id, sqlc.arg(request_id), sqlc.arg(source_major), sqlc.arg(target_major), sqlc.arg(database_name) FROM queued;

-- name: GetDatabaseUpgradeRehearsal :one
SELECT * FROM database_upgrade_rehearsals WHERE drill_id = sqlc.arg(drill_id);

-- name: SetDatabaseUpgradeRehearsalPhase :exec
UPDATE database_upgrade_rehearsals SET phase = sqlc.arg(phase) WHERE drill_id = sqlc.arg(drill_id);

-- name: SetDatabaseUpgradeRehearsalTarget :exec
WITH planned AS (
    UPDATE database_restore_drills d SET target_at = sqlc.arg(target_at)
    WHERE d.drill_id = sqlc.arg(drill_id) AND d.result = 'queued'
    RETURNING d.drill_id
)
UPDATE database_upgrade_rehearsals r SET recovery_timeline = sqlc.arg(recovery_timeline)
FROM planned p WHERE r.drill_id = p.drill_id;

-- name: IsDatabaseUpgradeRehearsalRestore :one
SELECT EXISTS (
    SELECT 1 FROM database_upgrade_rehearsals r
    JOIN database_restore_drills d USING (drill_id)
    WHERE d.drill_id = sqlc.arg(drill_id) AND d.source_id = sqlc.arg(source_id) AND d.target_at = sqlc.arg(target_at)
);

-- name: MarkDatabaseUpgradeRehearsalRestoring :exec
UPDATE database_upgrade_rehearsals SET restore_started = true WHERE drill_id = sqlc.arg(drill_id);

-- name: PinDatabaseVMRestoreTimeline :execrows
UPDATE database_vms SET restore_timeline = sqlc.arg(restore_timeline)
WHERE database_id = sqlc.arg(database_id)
AND state IN ('provisioning', 'rehearsal_restore') AND restore_source IS NOT NULL
AND restore_timeline IN (0, sqlc.arg(restore_timeline));
