-- filename: 20261115191402200_event_subscription_clone_paused.sql
-- migration-compat: expand
-- ADR-20261104120000 (amendment 2026-11-14): a full environment clone copies
-- the source's event subscriptions into the stage disabled, as it does crons.
-- clone_paused marks the copies that were enabled in the source, so resuming
-- the stage's automation enables exactly those and leaves a subscription the
-- source had disabled off. Any explicit change of enabled, by any writer
-- (a stage deploy reinstalling its manifest triggers included), clears the
-- mark: the customer has decided.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE event_subscriptions ADD COLUMN IF NOT EXISTS clone_paused boolean NOT NULL DEFAULT false;

CREATE OR REPLACE FUNCTION clear_event_subscription_clone_pause() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.enabled IS DISTINCT FROM OLD.enabled THEN
    NEW.clone_paused := false;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS clear_event_subscription_clone_pause ON event_subscriptions;
CREATE TRIGGER clear_event_subscription_clone_pause BEFORE UPDATE OF enabled ON event_subscriptions
  FOR EACH ROW EXECUTE FUNCTION clear_event_subscription_clone_pause();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS clear_event_subscription_clone_pause ON event_subscriptions;
DROP FUNCTION IF EXISTS clear_event_subscription_clone_pause();
ALTER TABLE event_subscriptions DROP COLUMN IF EXISTS clone_paused;
-- +goose StatementEnd
