-- filename: 20261002164048463_runtime_log_archive_collection_receipts.sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS runtime_log_archive_collections (
    instance_id text NOT NULL REFERENCES runtime_log_archive_bindings(instance_id) ON DELETE CASCADE,
    archive_day date NOT NULL CHECK (archive_day >= '0001-01-01' AND archive_day <= '9999-12-31'),
    collected_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(collected_at)),
    PRIMARY KEY (instance_id, archive_day)
);
CREATE OR REPLACE FUNCTION enforce_runtime_log_archive_collection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF (NEW.instance_id, NEW.archive_day, NEW.collected_at) IS DISTINCT FROM (OLD.instance_id, OLD.archive_day, OLD.collected_at) THEN
            RAISE EXCEPTION 'runtime log archive collection receipt is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_collection_immutable';
        END IF;
    ELSIF NOT EXISTS (
        SELECT 1 FROM runtime_log_archives r JOIN runtime_log_archive_bindings b ON b.instance_id = r.instance_id
        WHERE r.instance_id = NEW.instance_id AND r.archive_day = NEW.archive_day AND r.archive_day < b.expired_before
    ) THEN
        RAISE EXCEPTION 'runtime log archive collection requires its retired reservation'
            USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_collection_retired';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS runtime_log_archive_collection_guard ON runtime_log_archive_collections;
CREATE TRIGGER runtime_log_archive_collection_guard BEFORE INSERT OR UPDATE ON runtime_log_archive_collections
    FOR EACH ROW EXECUTE FUNCTION enforce_runtime_log_archive_collection();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $guard$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_log_archive_collections) THEN
        RAISE EXCEPTION 'cannot discard runtime log archive collection receipts';
    END IF;
END $guard$;
DROP TRIGGER runtime_log_archive_collection_guard ON runtime_log_archive_collections;
DROP FUNCTION enforce_runtime_log_archive_collection();
DROP TABLE runtime_log_archive_collections;
-- +goose StatementEnd
