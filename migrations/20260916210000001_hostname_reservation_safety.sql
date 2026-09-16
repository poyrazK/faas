-- +goose Up
-- Expired, unverified domain challenges are not ownership records. Remove the
-- existing backlog once; subsequent creates can also reclaim an expired row
-- atomically, so expiry always releases the global hostname key.
DELETE FROM custom_domains
WHERE verified_at IS NULL
  AND verification_expires_at <= now();

-- Handler validation gives customers an actionable 422. This trigger is the
-- final allocation guard for direct SQL, old binaries during a rolling deploy,
-- and any future app-creation path. Existing reserved rows remain readable and
-- updateable so an operator can migrate them; only INSERT and a slug change to
-- a reserved value are refused.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_reserved_app_slug_allocation() RETURNS trigger AS $$
BEGIN
    IF NEW.slug = ANY (ARRAY[
        'account','admin','api','assets','billing','cdn','console','dashboard',
        'docs','help','login','logout','mail','ns','operations','security',
        'signup','static','status','support','www'
    ]::text[])
       AND (TG_OP = 'INSERT' OR NEW.slug IS DISTINCT FROM OLD.slug) THEN
        RAISE EXCEPTION 'app slug % is reserved for a Gregale service', NEW.slug
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS apps_reserved_slug_allocation_guard ON apps;
CREATE TRIGGER apps_reserved_slug_allocation_guard
BEFORE INSERT OR UPDATE OF slug ON apps
FOR EACH ROW EXECUTE FUNCTION reject_reserved_app_slug_allocation();

-- +goose Down
DROP TRIGGER IF EXISTS apps_reserved_slug_allocation_guard ON apps;
DROP FUNCTION IF EXISTS reject_reserved_app_slug_allocation();
