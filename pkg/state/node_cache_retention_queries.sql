-- name: ListReferencedSnapshotCaptureKeys :many
-- ADR-20261114120000: every snapshot memory key a row still references, so
-- a compute node can drop cached captures nothing references. use is 'live'
-- for a non-stale snapshot (evicted last), 'stale' for a stale one (evicted
-- first) and 'other' for branch captures, crash captures and in-flight live
-- migrations, which reference captures outside the snapshots table.
SELECT storage_key::text AS key, (CASE WHEN stale THEN 'stale' ELSE 'live' END)::text AS use
  FROM snapshots
 WHERE storage_key LIKE 'snap/%'
UNION ALL
SELECT storage_key::text, 'other'::text
  FROM environment_branch_captures
 WHERE storage_key LIKE 'snap/%'
UNION ALL
SELECT storage_key::text, 'other'::text
  FROM environment_branch_capture_objects
 WHERE storage_key LIKE 'snap/%'
UNION ALL
SELECT storage_key::text, 'other'::text
  FROM crash_captures
 WHERE storage_key LIKE 'snap/%'
UNION ALL
SELECT mem_storage_key::text, 'other'::text
  FROM migration_admissions
 WHERE mem_storage_key LIKE 'snap/%'
UNION ALL
SELECT mem_storage_key::text, 'other'::text
  FROM migration_leases
 WHERE mem_storage_key LIKE 'snap/%';

-- name: ListRuntimeReleaseRetention :many
-- ADR-20261114120000: every runtime release, and whether a compute node must
-- keep its base cached: a deployment that is not failed or cancelled is
-- bound to it, or a runtime upgrade targets it.
SELECT r.id, r.runtime, r.architecture, r.created_at,
       (EXISTS (SELECT 1
                  FROM runtime_artifact_bindings b
                  JOIN deployments d ON d.rootfs_key = b.rootfs_key
                 WHERE b.release_id = r.id
                   AND d.status NOT IN ('failed', 'cancelled'))
        OR EXISTS (SELECT 1 FROM deployment_runtime_upgrade_targets t WHERE t.release_id = r.id)
        OR EXISTS (SELECT 1 FROM runtime_upgrade_operations o WHERE o.target_release_id = r.id))::bool AS keep
  FROM runtime_releases r;
