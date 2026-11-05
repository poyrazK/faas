-- filename: 20261026201726001_cron_schedule_anchor.sql
-- migration-compat: expand
-- ADR-20261026201726: a cron's next fire is the first boundary after the
-- later of last_fired_at and schedule_anchor_at. The crons trigger stamps the
-- anchor when the schedule or timezone changes, a disabled cron is enabled
-- again, or a suspension is lifted, so none of those fires the cron at once
-- or records the occurrences it passed while off. The previous release
-- neither reads nor writes the column.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE crons ADD COLUMN IF NOT EXISTS schedule_anchor_at timestamptz;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION revise_cron_schedule_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.schedule IS DISTINCT FROM OLD.schedule
    OR NEW.timezone IS DISTINCT FROM OLD.timezone
    OR NEW.schedule_policy IS DISTINCT FROM OLD.schedule_policy
    OR NEW.failure_rules IS DISTINCT FROM OLD.failure_rules THEN
  NEW.schedule_revision := OLD.schedule_revision + 1;
 END IF;
 IF NEW.schedule IS DISTINCT FROM OLD.schedule
    OR NEW.timezone IS DISTINCT FROM OLD.timezone
    OR (NEW.enabled AND NOT OLD.enabled)
    OR (NEW.suspended_reason = '' AND OLD.suspended_reason <> '') THEN
  NEW.schedule_anchor_at := clock_timestamp();
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION revise_cron_schedule_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.schedule IS DISTINCT FROM OLD.schedule
    OR NEW.timezone IS DISTINCT FROM OLD.timezone
    OR NEW.schedule_policy IS DISTINCT FROM OLD.schedule_policy
    OR NEW.failure_rules IS DISTINCT FROM OLD.failure_rules THEN
  NEW.schedule_revision := OLD.schedule_revision + 1;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER TABLE crons DROP COLUMN IF EXISTS schedule_anchor_at;
