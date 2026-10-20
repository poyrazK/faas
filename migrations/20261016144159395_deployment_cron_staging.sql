-- migration-compat: expand
-- Applied online (ADR-202610171900): the columns are added bare, their checks
-- NOT VALID and validated afterwards, each step its own short transaction.
-- Every step is idempotent.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE apps
 ADD COLUMN IF NOT EXISTS deployment_cron_revision bigint NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS deployment_cron_owner text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS deployment_cron_fingerprint text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS deployment_cron_before jsonb;

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_cron_revision_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_cron_revision_check CHECK (deployment_cron_revision >= 0) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_cron_owner_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_cron_owner_check CHECK (deployment_cron_owner = '' OR deployment_cron_owner ~ '^[0-9a-f]{32}$') NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_cron_fingerprint_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_cron_fingerprint_check CHECK (deployment_cron_fingerprint = '' OR deployment_cron_fingerprint ~ '^[0-9a-f]{64}$') NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_cron_before_check') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_cron_before_check CHECK (deployment_cron_before IS NULL OR jsonb_typeof(deployment_cron_before) = 'array') NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='apps'::regclass AND conname='apps_deployment_cron_ownership') THEN
  ALTER TABLE apps ADD CONSTRAINT apps_deployment_cron_ownership CHECK ((deployment_cron_owner = '') = (deployment_cron_fingerprint = '') AND (deployment_cron_owner = '') = (deployment_cron_before IS NULL)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_cron_revision_check;
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_cron_owner_check;
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_cron_fingerprint_check;
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_cron_before_check;
ALTER TABLE apps VALIDATE CONSTRAINT apps_deployment_cron_ownership;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_deployment_cron_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent record; old_app uuid; new_app uuid; http_intent boolean;
BEGIN
 IF TG_OP <> 'INSERT' THEN old_app := OLD.app_id; END IF;
 IF TG_OP <> 'DELETE' THEN new_app := NEW.app_id; END IF;
 http_intent := (TG_OP <> 'INSERT' AND cardinality(OLD.command) = 0)
             OR (TG_OP <> 'DELETE' AND cardinality(NEW.command) = 0);
 FOR parent IN SELECT id, account_id FROM apps WHERE id IN (old_app,new_app) ORDER BY account_id,id LOOP
  PERFORM 1 FROM accounts WHERE id = parent.account_id FOR UPDATE;
  IF http_intent THEN
   UPDATE apps SET deployment_cron_revision=deployment_cron_revision+1,
    deployment_cron_owner='', deployment_cron_fingerprint='', deployment_cron_before=NULL WHERE id=parent.id;
  END IF;
 END LOOP;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER crons_fence_deployment_intent AFTER INSERT OR DELETE OR UPDATE OF
 app_id,schedule,path,enabled,timezone,skip_if_running,command,command_shell,
 command_timeout_seconds,command_max_output_bytes,retry_max,retry_backoff_seconds,
 schedule_policy,failure_rules,created_at ON crons
 FOR EACH ROW EXECUTE FUNCTION fence_deployment_cron_intent();

-- +goose Down
DROP TRIGGER crons_fence_deployment_intent ON crons;
DROP FUNCTION fence_deployment_cron_intent();
ALTER TABLE apps DROP CONSTRAINT apps_deployment_cron_ownership,
 DROP COLUMN deployment_cron_before, DROP COLUMN deployment_cron_fingerprint,
 DROP COLUMN deployment_cron_owner, DROP COLUMN deployment_cron_revision;
