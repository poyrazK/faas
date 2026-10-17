-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_queue_release_requirements(requirements jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE entry record; count integer:=0;
BEGIN
 IF requirements IS NULL OR jsonb_typeof(requirements)<>'object' THEN RETURN false; END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(requirements) LOOP
  count:=count+1;
  IF count>32 OR entry.key !~ '^[a-z][a-z0-9-]{0,62}$'
   OR jsonb_typeof(entry.value)<>'string' OR entry.value #>> '{}' NOT IN ('consumer_readiness','handler_completion') THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS queue_binding_canaries (
 id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id),
 app_id uuid NOT NULL REFERENCES apps(id),
 binding_id uuid NOT NULL REFERENCES queue_bindings(id),
 trigger_id uuid NOT NULL,
 deployment_id uuid NOT NULL REFERENCES deployments(id),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 published_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>published_at AND expires_at<=published_at+interval '600 seconds'),
 cancelled boolean NOT NULL DEFAULT false,
 completed_deployment_id uuid REFERENCES deployments(id),
 completed_attempt integer CHECK(completed_attempt=1),
 completed_generation bigint CHECK(completed_generation=0),
 CHECK((completed_deployment_id IS NULL AND completed_attempt IS NULL AND completed_generation IS NULL) OR
       (completed_deployment_id IS NOT NULL AND completed_attempt IS NOT NULL AND completed_generation IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS queue_binding_canaries_lookup ON queue_binding_canaries(app_id,binding_id,deployment_id,published_at DESC);
-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='queue_binding_canaries'::regclass AND tgname='binding_promotion_revision') THEN
  CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON queue_binding_canaries FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,binding_id,trigger_id,deployment_id,snapshot,published_at,expires_at,cancelled,completed_deployment_id,completed_attempt,completed_generation');
 END IF;
END $replay$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM app_binding_release_policies WHERE EXISTS(SELECT 1 FROM jsonb_each_text(queue_requirements) e WHERE e.value='handler_completion')) THEN
 RAISE EXCEPTION 'clear handler_completion policies before rollback';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_queue_release_requirements(requirements jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE entry record; count integer:=0;
BEGIN
 IF requirements IS NULL OR jsonb_typeof(requirements)<>'object' THEN RETURN false; END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(requirements) LOOP
  count:=count+1;
  IF count>32 OR entry.key !~ '^[a-z][a-z0-9-]{0,62}$'
   OR jsonb_typeof(entry.value)<>'string' OR entry.value #>> '{}' <> 'consumer_readiness' THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;
-- +goose StatementEnd

DROP TABLE queue_binding_canaries;
