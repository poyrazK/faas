-- filename: 20261119071143100_builds_preemptions.sql
-- migration-compat: expand
-- ADR-20261118154632: elastic builder slots.

-- +goose Up
--
-- A borrowed builder slot gives its build back when tenants need the memory:
-- builderd (or vmmd) stops the build's VM and the build returns to the
-- queue at its original position. builds.preemptions counts those
-- give-backs. A build given back api.BuildMaxPreemptions times runs only on
-- a reserved slot, so it cannot bounce between borrowed slots forever. The
-- column defaults to 0, so a release that does not know it writes nothing
-- and claims it the same way.
SET LOCAL lock_timeout = '5s';

ALTER TABLE builds ADD COLUMN IF NOT EXISTS preemptions integer DEFAULT 0 NOT NULL;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'builds_preemptions_check') THEN
        ALTER TABLE builds ADD CONSTRAINT builds_preemptions_check CHECK (preemptions >= 0) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE builds DROP CONSTRAINT IF EXISTS builds_preemptions_check;
ALTER TABLE builds DROP COLUMN IF EXISTS preemptions;
