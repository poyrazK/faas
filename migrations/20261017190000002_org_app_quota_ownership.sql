-- +goose Up
-- Replay completed expansion without replacing functions refined by later migrations.
-- A partially present expansion still fails atomically and requires repair.
-- +goose StatementBegin
DO $org_ownership_replay$
BEGIN
 IF to_regclass('apps_org_quota_idx') IS NOT NULL
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='bind_app_quota_owner')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_account_shared_app_purge')
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('apps') AND tgname='app_quota_owner_insert' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('accounts') AND tgname='account_shared_app_purge' AND NOT tgisinternal) THEN
  RETURN;
 END IF;

-- This changes app admission ownership, not provider billing attribution.
-- Existing shared OrgIDs are authoritative and must never be reassigned.
UPDATE apps a SET org_id = o.id FROM orgs o
WHERE a.org_id IS NULL AND o.personal_org AND o.personal_owner_account_id = a.account_id;

CREATE INDEX apps_org_quota_idx ON apps (org_id)
WHERE status IN ('active', 'evicted_cold');

CREATE FUNCTION bind_app_quota_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE personal_owner uuid; is_personal boolean;
BEGIN
    IF NEW.org_id IS NULL THEN
        SELECT id INTO NEW.org_id FROM orgs
        WHERE personal_org AND personal_owner_account_id = NEW.account_id;
    END IF;
    IF NEW.org_id IS NOT NULL THEN
        SELECT personal_org, personal_owner_account_id INTO is_personal, personal_owner
        FROM orgs WHERE id = NEW.org_id FOR SHARE;
        IF is_personal AND personal_owner IS DISTINCT FROM NEW.account_id THEN
            RAISE EXCEPTION 'app personal organization does not match account' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER app_quota_owner_insert BEFORE INSERT ON apps
FOR EACH ROW EXECUTE FUNCTION bind_app_quota_owner();

-- Hard purge must fail atomically while an account is still the legacy
-- storage/billing root of a company app, including restore-window tombstones.
CREATE FUNCTION guard_account_shared_app_purge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id = a.org_id
               WHERE a.account_id = OLD.id AND NOT o.personal_org) THEN
        RAISE EXCEPTION 'shared app ownership migration required before account purge'
            USING ERRCODE = '23514', CONSTRAINT = 'account_shared_app_ownership_pending';
    END IF;
    RETURN OLD;
END $$;
CREATE TRIGGER account_shared_app_purge BEFORE DELETE ON accounts
FOR EACH ROW EXECUTE FUNCTION guard_account_shared_app_purge();
END;
$org_ownership_replay$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER account_shared_app_purge ON accounts;
DROP FUNCTION guard_account_shared_app_purge();
DROP TRIGGER app_quota_owner_insert ON apps;
DROP FUNCTION bind_app_quota_owner();
DROP INDEX apps_org_quota_idx;
-- Forward-only attribution repair is retained on rollback.
