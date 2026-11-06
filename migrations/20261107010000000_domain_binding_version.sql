-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains ADD COLUMN binding_version uuid;
ALTER TABLE custom_domains ALTER COLUMN binding_version SET DEFAULT gen_random_uuid();

-- Older control planes do not know this column. Fence their reclaims too,
-- including deterministic account-bound DNS-proof tokens reused after takeover.
-- +goose StatementBegin
CREATE FUNCTION custom_domain_binding_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.app_id, OLD.environment_id, OLD.challenge_token) IS DISTINCT FROM
       (NEW.app_id, NEW.environment_id, NEW.challenge_token) THEN
        NEW.binding_version := gen_random_uuid();
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER custom_domain_binding_version BEFORE UPDATE OF app_id, environment_id, challenge_token
ON custom_domains FOR EACH ROW EXECUTE FUNCTION custom_domain_binding_version();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER custom_domain_binding_version ON custom_domains;
DROP FUNCTION custom_domain_binding_version();
ALTER TABLE custom_domains DROP COLUMN binding_version;
