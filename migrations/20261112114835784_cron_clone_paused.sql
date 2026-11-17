-- filename: 20261112114835784_cron_clone_paused.sql
-- migration-compat: expand
-- ADR-20261104120000 (amendment 2026-11-12): a full environment clone copies
-- the source's crons into the stage disabled. clone_paused marks the copies
-- that were enabled in the source, so resuming the stage's automation enables
-- exactly those and leaves a cron the source had disabled off. Any explicit
-- change of enabled, by any writer, clears the mark: the customer has decided.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE crons ADD COLUMN IF NOT EXISTS clone_paused boolean NOT NULL DEFAULT false;

CREATE OR REPLACE FUNCTION clear_cron_clone_pause() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.enabled IS DISTINCT FROM OLD.enabled THEN
    NEW.clone_paused := false;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS clear_cron_clone_pause ON crons;
CREATE TRIGGER clear_cron_clone_pause BEFORE UPDATE OF enabled ON crons
  FOR EACH ROW EXECUTE FUNCTION clear_cron_clone_pause();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS clear_cron_clone_pause ON crons;
DROP FUNCTION IF EXISTS clear_cron_clone_pause();
ALTER TABLE crons DROP COLUMN IF EXISTS clone_paused;
-- +goose StatementEnd
