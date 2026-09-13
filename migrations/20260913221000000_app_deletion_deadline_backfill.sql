-- +goose Up
-- +goose StatementBegin

-- Legacy app tombstones predate the restore deadline columns. Give every
-- existing row a fresh, conservative seven-day restore window from migration
-- time because the original deletion instant cannot be reconstructed safely.
UPDATE apps
   SET deleted_at = coalesce(deleted_at, now()),
       delete_grace_until = coalesce(delete_grace_until, now() + interval '7 days')
 WHERE status = 'deleted'
   AND (deleted_at IS NULL OR delete_grace_until IS NULL);

-- Old rootfs rows may have only the absolute local path. Recover the portable
-- StorageBackend key before the grace worker starts deleting artifacts.
UPDATE deployments d
   SET rootfs_key = 'apps/' || a.slug || '/' || regexp_replace(d.rootfs_path, '^.*/', '')
  FROM apps a
 WHERE d.app_id = a.id
   AND d.rootfs_key = ''
   AND coalesce(d.rootfs_path, '') <> ''
   AND regexp_replace(d.rootfs_path, '^.*/', '') <> '';

-- This trigger is the database backstop for every current and future delete
-- path, including direct maintenance SQL. Go writers also stamp the fields so
-- RETURNING observes a complete tombstone without depending on trigger order.
CREATE OR REPLACE FUNCTION faas_stamp_app_deletion_deadline()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'deleted' THEN
        NEW.deleted_at := coalesce(NEW.deleted_at, now());
        NEW.delete_grace_until := coalesce(NEW.delete_grace_until, now() + interval '7 days');
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_stamp_deletion_deadline ON apps;
CREATE TRIGGER apps_stamp_deletion_deadline
BEFORE INSERT OR UPDATE OF status ON apps
FOR EACH ROW EXECUTE FUNCTION faas_stamp_app_deletion_deadline();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS apps_stamp_deletion_deadline ON apps;
DROP FUNCTION IF EXISTS faas_stamp_app_deletion_deadline();
-- Backfilled deadlines and portable rootfs keys are intentionally retained.
-- +goose StatementEnd
