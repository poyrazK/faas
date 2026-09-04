-- filename: 20260904000000001_migration_notify_every_apply.sql
-- ADR-142: out-of-order timestamp migrations can land below MAX(version_id),
-- so every applied ledger insert must wake exact-set waiters.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION migration_notify() RETURNS trigger AS $$
BEGIN
    IF NEW.is_applied IS TRUE THEN
        PERFORM pg_notify('migrations_applied', NEW.version_id::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION migration_notify() RETURNS trigger AS $$
BEGIN
    IF NEW.is_applied IS DISTINCT FROM true THEN
        RETURN NEW;
    END IF;
    IF NEW.version_id = (SELECT MAX(version_id) FROM goose_db_version) THEN
        PERFORM pg_notify('migrations_applied', NEW.version_id::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
