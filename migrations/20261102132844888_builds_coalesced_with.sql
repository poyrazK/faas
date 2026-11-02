-- migration-compat: expand
-- ADR-20261102100000: a build that misses the cache while an identical build
-- (same cache key) is already building waits for it instead of building the
-- same image again. coalesced_with names that build; claims skip the waiter
-- while it is queued or running, then prefer the node that built it. No
-- foreign key: a dangling id only means there is nothing left to wait for,
-- and deleting builds must not scan for references. The new column is NULL in
-- every existing row, so the CHECK is added NOT VALID: new writes are checked
-- at once and no existing row needs a scan under the lock.
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE builds ADD COLUMN IF NOT EXISTS coalesced_with uuid;
-- +goose StatementBegin
DO $coalesced$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'builds'::regclass AND conname = 'builds_coalesced_with_check') THEN
    ALTER TABLE builds ADD CONSTRAINT builds_coalesced_with_check
      CHECK (coalesced_with IS NULL OR coalesced_with <> id) NOT VALID;
  END IF;
END
$coalesced$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE builds DROP COLUMN IF EXISTS coalesced_with;
