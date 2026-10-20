-- migration-compat: expand
-- Applied online (ADR-202610171900): the columns are added bare, their checks
-- NOT VALID and validated afterwards, each step its own short transaction.
-- Every step is idempotent.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS deployment_policy_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS deployment_policy_owner text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_policy_fingerprint text NOT NULL DEFAULT '';

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_policy_revision_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_policy_revision_check CHECK (deployment_policy_revision >= 0) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_policy_owner_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_policy_owner_check CHECK (deployment_policy_owner = '' OR deployment_policy_owner ~ '^[0-9a-f]{32}$') NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_policy_fingerprint_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_policy_fingerprint_check CHECK (deployment_policy_fingerprint = '' OR deployment_policy_fingerprint ~ '^[0-9a-f]{64}$') NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_policy_ownership_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_policy_ownership_check CHECK ((deployment_policy_owner = '') = (deployment_policy_fingerprint = '')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_policy_revision_check;
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_policy_owner_check;
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_policy_fingerprint_check;
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_policy_ownership_check;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_deployment_policy_changes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF coalesce(OLD.manifest->'profiling','null'::jsonb) IS DISTINCT FROM coalesce(NEW.manifest->'profiling','null'::jsonb)
       OR coalesce(OLD.manifest->'tracing','null'::jsonb) IS DISTINCT FROM coalesce(NEW.manifest->'tracing','null'::jsonb) THEN
        IF NEW.deployment_policy_revision = OLD.deployment_policy_revision THEN
            NEW.deployment_policy_revision := OLD.deployment_policy_revision + 1;
        END IF;
        IF NEW.deployment_policy_owner = OLD.deployment_policy_owner THEN
            NEW.deployment_policy_owner := '';
            NEW.deployment_policy_fingerprint := '';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER apps_fence_deployment_policy_changes BEFORE UPDATE OF manifest ON apps
    FOR EACH ROW EXECUTE FUNCTION fence_deployment_policy_changes();

-- +goose Down
DROP TRIGGER apps_fence_deployment_policy_changes ON apps;
DROP FUNCTION fence_deployment_policy_changes();
ALTER TABLE apps DROP CONSTRAINT apps_deployment_policy_ownership_check,
    DROP COLUMN deployment_policy_revision, DROP COLUMN deployment_policy_owner, DROP COLUMN deployment_policy_fingerprint;
