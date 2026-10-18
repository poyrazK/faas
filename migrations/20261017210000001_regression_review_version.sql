-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE debug_regression_observations
    ADD COLUMN IF NOT EXISTS review_version uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'::uuid;

-- Replaying an earlier migration must preserve later constraints and fences.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'debug_regression_observations'::regclass
                     AND conname = 'debug_regression_review_version_nonzero') THEN
        ALTER TABLE debug_regression_observations
            ADD CONSTRAINT debug_regression_review_version_nonzero CHECK (review_version <> '00000000-0000-0000-0000-000000000000'::uuid);
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- The nonvolatile initial version uses PostgreSQL fast defaults for existing rows.
-- Versions are compared together with the complete immutable row key.
-- Every writer, including N-1 detectors and legacy triage clients, invalidates reviews.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_proc
                   WHERE pronamespace = current_schema()::regnamespace
                     AND proname = 'advance_regression_review_version'
                     AND pronargs = 0) THEN
        CREATE FUNCTION advance_regression_review_version() RETURNS trigger
        LANGUAGE plpgsql AS $function$
        BEGIN
            NEW.review_version := gen_random_uuid();
            RETURN NEW;
        END;
        $function$;
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger
                   WHERE tgrelid = 'debug_regression_observations'::regclass
                     AND tgname = 'regression_review_version') THEN
        CREATE TRIGGER regression_review_version BEFORE INSERT OR UPDATE ON debug_regression_observations
            FOR EACH ROW EXECUTE FUNCTION advance_regression_review_version();
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER regression_review_version ON debug_regression_observations;
DROP FUNCTION advance_regression_review_version();
ALTER TABLE debug_regression_observations DROP COLUMN review_version;
