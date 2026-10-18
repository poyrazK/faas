-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE debug_regression_observations
    ADD COLUMN review_version uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'::uuid,
    ADD CONSTRAINT debug_regression_review_version_nonzero CHECK (review_version <> '00000000-0000-0000-0000-000000000000'::uuid);

-- The nonvolatile initial version uses PostgreSQL fast defaults for existing rows.
-- Versions are compared together with the complete immutable row key.
-- Every writer, including N-1 detectors and legacy triage clients, invalidates reviews.
-- +goose StatementBegin
CREATE FUNCTION advance_regression_review_version() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.review_version := gen_random_uuid();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER regression_review_version BEFORE INSERT OR UPDATE ON debug_regression_observations
    FOR EACH ROW EXECUTE FUNCTION advance_regression_review_version();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER regression_review_version ON debug_regression_observations;
DROP FUNCTION advance_regression_review_version();
ALTER TABLE debug_regression_observations DROP COLUMN review_version;
