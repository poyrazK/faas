-- +goose Up
-- +goose StatementBegin

-- A ready replica is useful only while its snapshot can still participate in
-- a customer wake. App deletion removes the corresponding shared artifacts,
-- and failed/cancelled deployments are never rollback targets. Retire those
-- snapshots at the lifecycle boundary so fan-out workers do not spend cache
-- capacity and registry requests on objects that cannot serve traffic.

CREATE OR REPLACE FUNCTION snapshot_replica_drop_after_stale()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    DELETE FROM snapshot_replicas
     WHERE snapshot_id = NEW.id;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS snapshot_replica_drop_after_stale ON snapshots;
CREATE TRIGGER snapshot_replica_drop_after_stale
    AFTER UPDATE OF stale ON snapshots
    FOR EACH ROW
    WHEN (NEW.stale = true AND OLD.stale IS DISTINCT FROM NEW.stale)
    EXECUTE FUNCTION snapshot_replica_drop_after_stale();

CREATE OR REPLACE FUNCTION snapshot_stale_after_terminal_deployment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE snapshots
       SET stale = true
     WHERE deployment_id = NEW.id
       AND stale = false;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS snapshot_stale_after_terminal_deployment ON deployments;
CREATE TRIGGER snapshot_stale_after_terminal_deployment
    AFTER UPDATE OF status ON deployments
    FOR EACH ROW
    WHEN (
        NEW.status IN ('failed', 'cancelled')
        AND OLD.status IS DISTINCT FROM NEW.status
    )
    EXECUTE FUNCTION snapshot_stale_after_terminal_deployment();

CREATE OR REPLACE FUNCTION snapshot_stale_after_app_delete()
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

DROP TRIGGER IF EXISTS snapshot_stale_after_app_delete ON apps;
CREATE TRIGGER snapshot_stale_after_app_delete
    AFTER UPDATE OF status ON apps
    FOR EACH ROW
    WHEN (
        NEW.status = 'deleted'
        AND OLD.status IS DISTINCT FROM NEW.status
    )
    EXECUTE FUNCTION snapshot_stale_after_app_delete();

-- Repair rows created before the lifecycle triggers existed. Superseded
-- deployments remain fresh because the product keeps them for rollback.
UPDATE snapshots s
   SET stale = true
  FROM deployments d
  JOIN apps a ON a.id = d.app_id
 WHERE s.deployment_id = d.id
   AND s.stale = false
   AND (d.status IN ('failed', 'cancelled') OR a.status = 'deleted');

-- Also clean any pre-existing replica rows for snapshots that were already
-- stale before this migration. The delete is idempotent and FK-safe.
DELETE FROM snapshot_replicas r
 USING snapshots s
 WHERE r.snapshot_id = s.id
   AND s.stale = true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS snapshot_stale_after_app_delete ON apps;
DROP FUNCTION IF EXISTS snapshot_stale_after_app_delete();
DROP TRIGGER IF EXISTS snapshot_stale_after_terminal_deployment ON deployments;
DROP FUNCTION IF EXISTS snapshot_stale_after_terminal_deployment();
DROP TRIGGER IF EXISTS snapshot_replica_drop_after_stale ON snapshots;
DROP FUNCTION IF EXISTS snapshot_replica_drop_after_stale();
-- Lifecycle data changes are intentionally not reversed: making a stale
-- snapshot fresh again would advertise cache objects that may have been GC'd.
-- +goose StatementEnd
