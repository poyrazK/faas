-- Source spool retention (ADR-20261026152731). A deployment that can still
-- build or image reads its source archive from the build spool and appends
-- to its build log there. The spool sweeper never removes either while the
-- deployment is in one of these states, whatever the file's age.

-- name: ListInFlightDeploymentSpoolPaths :many
SELECT COALESCE(source_path, '')::text AS source_path, COALESCE(log_path, '')::text AS log_path
FROM deployments
WHERE status IN ('pending', 'building', 'imaging', 'snapshotting');
