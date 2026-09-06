-- +goose Up
-- +goose StatementBegin

-- A Firecracker memory snapshot has the guest RAM layout captured at boot.
-- Restoring it after apps.ram_mb changes would give an upgraded app the old
-- smaller guest, or try to run a down-sized app from an oversized image.
-- Invalidate those snapshots in the same transaction as the app update.
CREATE OR REPLACE FUNCTION snapshot_stale_after_app_ram_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE snapshots s
       SET stale = true
      FROM deployments d
     WHERE s.deployment_id = d.id
       AND d.app_id = NEW.id
       AND s.stale = false;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS snapshot_stale_after_app_ram_change ON apps;
CREATE TRIGGER snapshot_stale_after_app_ram_change
    AFTER UPDATE OF ram_mb ON apps
    FOR EACH ROW
    WHEN (OLD.ram_mb IS DISTINCT FROM NEW.ram_mb)
    EXECUTE FUNCTION snapshot_stale_after_app_ram_change();

-- Repair snapshots captured under a memory size that no longer matches the
-- app. mem_bytes is the logical Firecracker guest-memory length and therefore
-- must equal ram_mb MiB for a compatible restore.
UPDATE snapshots s
   SET stale = true
  FROM deployments d
  JOIN apps a ON a.id = d.app_id
 WHERE s.deployment_id = d.id
   AND s.stale = false
   AND s.mem_bytes <> a.ram_mb::bigint * 1048576;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS snapshot_stale_after_app_ram_change ON apps;
DROP FUNCTION IF EXISTS snapshot_stale_after_app_ram_change();
-- Snapshot invalidation is deliberately irreversible: replicas may already
-- have been evicted by snapshot_replica_drop_after_stale.
-- +goose StatementEnd
