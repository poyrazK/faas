-- filename: 20261101091043072_app_slug_holds.sql
-- migration-compat: expand
--
-- ADR-20261101091049 (bug hunt 2026-10-28 H80): a rename or a purge freed an
-- app's slug for any account the moment it committed, so another tenant could
-- create an app with the old name and receive everything still pointing at
-- the old host: links, third-party webhooks, OAuth redirects and the cookies
-- the original app set. The old slug is now held for its account for 90 days
-- (api.AppSlugHoldDuration): the owner may reuse it, any other account gets
-- the slug-taken conflict. Older writers are unaffected; the triggers record
-- and enforce holds for every writer.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_slug_holds (
    slug        text PRIMARY KEY,
    account_id  uuid NOT NULL,
    held_until  timestamptz NOT NULL,
    reason      text NOT NULL CHECK (reason IN ('renamed', 'purged'))
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_slug_hold_check() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM app_slug_holds
        WHERE slug = NEW.slug AND account_id <> NEW.account_id AND held_until > now()
    ) THEN
        RAISE EXCEPTION 'app slug % is held for its previous owner', NEW.slug
            USING ERRCODE = 'unique_violation', CONSTRAINT = 'apps_slug_key';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_slug_hold_record() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.slug IS NOT DISTINCT FROM NEW.slug THEN
        RETURN NULL;
    END IF;
    INSERT INTO app_slug_holds (slug, account_id, held_until, reason)
    VALUES (OLD.slug, OLD.account_id, now() + interval '90 days',
            CASE TG_OP WHEN 'DELETE' THEN 'purged' ELSE 'renamed' END)
    ON CONFLICT (slug) DO UPDATE
        SET account_id = EXCLUDED.account_id,
            held_until = EXCLUDED.held_until,
            reason = EXCLUDED.reason;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS apps_slug_hold_check ON apps;
CREATE TRIGGER apps_slug_hold_check
    BEFORE INSERT OR UPDATE OF slug ON apps
    FOR EACH ROW EXECUTE FUNCTION app_slug_hold_check();

DROP TRIGGER IF EXISTS apps_slug_hold_record ON apps;
CREATE TRIGGER apps_slug_hold_record
    AFTER UPDATE OF slug OR DELETE ON apps
    FOR EACH ROW EXECUTE FUNCTION app_slug_hold_record();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS apps_slug_hold_record ON apps;
DROP TRIGGER IF EXISTS apps_slug_hold_check ON apps;
DROP FUNCTION IF EXISTS app_slug_hold_record();
DROP FUNCTION IF EXISTS app_slug_hold_check();
DROP TABLE IF EXISTS app_slug_holds;
