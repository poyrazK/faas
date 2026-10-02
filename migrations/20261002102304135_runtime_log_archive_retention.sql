-- filename: 20261002102304135_runtime_log_archive_retention.sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE runtime_log_archive_bindings ADD COLUMN IF NOT EXISTS expired_before date NOT NULL DEFAULT '0001-01-01'
    CHECK (expired_before >= '0001-01-01' AND expired_before <= '9999-12-31');
CREATE INDEX IF NOT EXISTS runtime_log_archives_retention ON runtime_log_archives(archive_day, instance_id);
CREATE OR REPLACE FUNCTION enforce_runtime_log_archive_retention_floor() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.expired_before < OLD.expired_before THEN
        RAISE EXCEPTION 'runtime log archive expiry cannot be reversed'
            USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_expiry_monotonic';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS runtime_log_archive_retention_floor_guard ON runtime_log_archive_bindings;
CREATE TRIGGER runtime_log_archive_retention_floor_guard BEFORE UPDATE OF expired_before ON runtime_log_archive_bindings
    FOR EACH ROW EXECUTE FUNCTION enforce_runtime_log_archive_retention_floor();
CREATE OR REPLACE FUNCTION enforce_runtime_log_archive_retention() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_log_archive_bindings b WHERE b.instance_id = NEW.instance_id AND NEW.archive_day < b.expired_before) THEN
        RAISE EXCEPTION 'runtime log archive day has expired'
            USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_expired';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS runtime_log_archive_retention_guard ON runtime_log_archives;
CREATE TRIGGER runtime_log_archive_retention_guard BEFORE INSERT OR UPDATE ON runtime_log_archives
    FOR EACH ROW EXECUTE FUNCTION enforce_runtime_log_archive_retention();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $guard$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_log_archive_bindings WHERE expired_before > '0001-01-01') THEN
        RAISE EXCEPTION 'cannot discard runtime log archive expiry fences';
    END IF;
END $guard$;
DROP TRIGGER runtime_log_archive_retention_guard ON runtime_log_archives;
DROP FUNCTION enforce_runtime_log_archive_retention();
DROP TRIGGER runtime_log_archive_retention_floor_guard ON runtime_log_archive_bindings;
DROP FUNCTION enforce_runtime_log_archive_retention_floor();
DROP INDEX runtime_log_archives_retention;
ALTER TABLE runtime_log_archive_bindings DROP COLUMN expired_before;
-- +goose StatementEnd
