-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains ADD COLUMN IF NOT EXISTS binding_version uuid;
ALTER TABLE custom_domains ALTER COLUMN binding_version SET DEFAULT gen_random_uuid();

-- Older control planes do not know this column. Fence their reclaims too,
-- including deterministic account-bound DNS-proof tokens reused after takeover.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF to_regprocedure('custom_domain_binding_version()') IS NULL THEN
CREATE FUNCTION custom_domain_binding_version() RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF (OLD.app_id, OLD.environment_id, OLD.challenge_token) IS DISTINCT FROM
       (NEW.app_id, NEW.environment_id, NEW.challenge_token) THEN
        NEW.binding_version := gen_random_uuid();
    END IF;
    RETURN NEW;
END;
$function$;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger
                   WHERE tgrelid = 'custom_domains'::regclass
                     AND tgname = 'custom_domain_binding_version') THEN
CREATE TRIGGER custom_domain_binding_version BEFORE UPDATE OF app_id, environment_id, challenge_token
ON custom_domains FOR EACH ROW EXECUTE FUNCTION custom_domain_binding_version();
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER custom_domain_binding_version ON custom_domains;
DROP FUNCTION custom_domain_binding_version();
ALTER TABLE custom_domains DROP COLUMN binding_version;
