-- filename: 20261128135953200_stage_identity_revocation_cascade.sql
-- migration-compat: expand
-- ADR-20261104120000 amendment 2026-11-28: a full clone copies production's
-- active API consumers and keys into a stage under the same external
-- reference and key prefix, and the copied key keeps its secret. Revoking
-- the production consumer or key revokes those copies too, whatever path
-- revokes it (N-1 included), so a revoked credential never keeps working
-- on a stage host.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

CREATE OR REPLACE FUNCTION cascade_production_consumer_key_revocation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE consumer_keys k SET revoked_at = NEW.revoked_at
   WHERE k.app_id = NEW.app_id AND k.prefix = NEW.prefix
     AND k.environment_id IS NOT NULL AND k.revoked_at IS NULL;
  RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS consumer_key_revocation_cascade ON consumer_keys;
CREATE TRIGGER consumer_key_revocation_cascade AFTER UPDATE OF revoked_at ON consumer_keys
  FOR EACH ROW
  WHEN (OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL AND NEW.environment_id IS NULL)
  EXECUTE FUNCTION cascade_production_consumer_key_revocation();

CREATE OR REPLACE FUNCTION cascade_production_api_consumer_revocation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE api_consumers c SET status = 'revoked', revoked_at = NEW.revoked_at, updated_at = now()
   WHERE c.app_id = NEW.app_id AND c.external_ref = NEW.external_ref
     AND c.environment_id IS NOT NULL AND c.revoked_at IS NULL;
  RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS api_consumer_revocation_cascade ON api_consumers;
CREATE TRIGGER api_consumer_revocation_cascade AFTER UPDATE OF revoked_at ON api_consumers
  FOR EACH ROW
  WHEN (OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL AND NEW.environment_id IS NULL)
  EXECUTE FUNCTION cascade_production_api_consumer_revocation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS api_consumer_revocation_cascade ON api_consumers;
DROP FUNCTION IF EXISTS cascade_production_api_consumer_revocation();
DROP TRIGGER IF EXISTS consumer_key_revocation_cascade ON consumer_keys;
DROP FUNCTION IF EXISTS cascade_production_consumer_key_revocation();
-- +goose StatementEnd
