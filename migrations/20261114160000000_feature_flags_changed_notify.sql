-- filename: 20261114160000000_feature_flags_changed_notify.sql
-- migration-compat: expand

-- ADR 20261114150000 §3: gatewayd-internal caches each deployment's flag
-- kill list and empties the cache on feature_flags_changed. Every writer of a
-- flag configuration (apply, rollback, promotion, clone, auto-advance) inserts
-- a feature_flag_versions row, so one insert trigger covers them all. The
-- payload is the environment id only; flag contents never leave the database.
-- An older gatewayd does not LISTEN on the channel and is unaffected.

-- +goose Up
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION feature_flag_versions_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('feature_flags_changed', NEW.environment_id::text);
    RETURN NULL;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS feature_flag_versions_notify ON feature_flag_versions;
CREATE TRIGGER feature_flag_versions_notify
    AFTER INSERT ON feature_flag_versions
    FOR EACH ROW EXECUTE FUNCTION feature_flag_versions_notify();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS feature_flag_versions_notify ON feature_flag_versions;
DROP FUNCTION IF EXISTS feature_flag_versions_notify();
