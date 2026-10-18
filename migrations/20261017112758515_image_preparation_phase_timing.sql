-- migration-compat: expand
-- filename: 20261017112758515_image_preparation_phase_timing.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Do not backfill: updated_at includes claims that made no phase progress.
ALTER TABLE deployment_image_preparations
    ADD COLUMN IF NOT EXISTS phase_started_at timestamptz
    CHECK (phase_started_at IS NULL OR (isfinite(phase_started_at) AND phase_started_at > 'epoch'::timestamptz));

-- Maintain timing for both current and older writers during rolling upgrades.
-- Same-phase claims cannot renew a known age or invent legacy history.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION image_preparation_phase_timing() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.phase_started_at := clock_timestamp();
    ELSIF NEW.phase IS DISTINCT FROM OLD.phase THEN
        NEW.phase_started_at := clock_timestamp();
    ELSE
        NEW.phase_started_at := OLD.phase_started_at;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER image_preparation_phase_timing
    BEFORE INSERT OR UPDATE ON deployment_image_preparations
    FOR EACH ROW EXECUTE FUNCTION image_preparation_phase_timing();

-- +goose Down
DROP TRIGGER IF EXISTS image_preparation_phase_timing ON deployment_image_preparations;
DROP FUNCTION IF EXISTS image_preparation_phase_timing();
ALTER TABLE deployment_image_preparations DROP COLUMN phase_started_at;
