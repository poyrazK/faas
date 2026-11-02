-- filename: 20261029161405023_org_release_freezes.sql
-- migration-compat: expand
-- ADR-20261019071733 amendment 4: a company organization owns the freezes of
-- its apps. A freeze belongs to an account, covering that account's personal
-- apps, or to an organization, covering the organization's apps; never both.
-- An organization's freezes outlive the member who created them, so they
-- leave account_id null and are removed only with the organization.
--
-- The previous release reads freezes by account_id alone, so it never sees an
-- organization's rows, and every row it writes has an account_id.
--
-- Each step commits on its own: the owner CHECK is added NOT VALID and then
-- validated, and the index is built concurrently.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE release_freezes ADD COLUMN IF NOT EXISTS org_id uuid REFERENCES orgs(id) ON DELETE CASCADE;
ALTER TABLE release_freezes ALTER COLUMN account_id DROP NOT NULL;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'release_freezes'::regclass AND conname = 'release_freezes_one_owner_chk') THEN
  ALTER TABLE release_freezes ADD CONSTRAINT release_freezes_one_owner_chk
    CHECK ((account_id IS NULL) <> (org_id IS NULL)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE release_freezes VALIDATE CONSTRAINT release_freezes_one_owner_chk;
CREATE INDEX CONCURRENTLY IF NOT EXISTS release_freezes_org_unlifted
 ON release_freezes(org_id, created_at, id) WHERE lifted_at IS NULL AND org_id IS NOT NULL;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- The previous schema has no owner but an account: an organization's freezes
-- cannot be kept.
DELETE FROM release_freezes WHERE org_id IS NOT NULL;
DROP INDEX CONCURRENTLY IF EXISTS release_freezes_org_unlifted;
ALTER TABLE release_freezes DROP CONSTRAINT IF EXISTS release_freezes_one_owner_chk;
ALTER TABLE release_freezes DROP COLUMN IF EXISTS org_id;
ALTER TABLE release_freezes ALTER COLUMN account_id SET NOT NULL;
RESET lock_timeout;
