-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1746. Legacy intent and task writers must not bypass the unsupported
-- producer boundary. Serialize with policy activation on the same account row.
CREATE OR REPLACE FUNCTION financial_compute_require_job_support(target_account uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM accounts WHERE id=target_account FOR UPDATE;
 IF EXISTS(SELECT 1 FROM financial_budget_policies p WHERE p.account_id=target_account
   AND p.deleted_at IS NULL AND p.spec->>'enabled'='true' AND p.spec->>'mode'='strict'
   AND p.spec->'scope'->>'kind'='account') THEN
  RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='financial_compute_account_job_supported',
   MESSAGE='independent jobs do not support strict account compute';
 END IF;
END
$$;
CREATE OR REPLACE FUNCTION financial_compute_guard_job_definition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='deleted' THEN RETURN NEW; END IF;
 PERFORM financial_compute_require_job_support(NEW.account_id);
 RETURN NEW;
END
$$;
CREATE OR REPLACE TRIGGER financial_compute_job_definition_guard BEFORE INSERT OR UPDATE OF status,account_id ON jobs
 FOR EACH ROW EXECUTE FUNCTION financial_compute_guard_job_definition();

-- Terminal task transitions remain available for cleanup. A previously failed
-- task cannot race activation by becoming queued or claimed after its job was
-- deleted, even when an old retry writer omits current application admission.
CREATE OR REPLACE FUNCTION financial_compute_guard_job_task() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE target_account uuid;
BEGIN
 IF NEW.status NOT IN ('queued','claimed') THEN RETURN NEW; END IF;
 SELECT account_id INTO target_account FROM job_runs WHERE id=NEW.run_id;
 IF target_account IS NOT NULL THEN
  PERFORM financial_compute_require_job_support(target_account);
 END IF;
 RETURN NEW;
END
$$;
CREATE OR REPLACE TRIGGER financial_compute_job_task_guard BEFORE INSERT OR UPDATE OF status,run_id ON job_tasks
 FOR EACH ROW EXECUTE FUNCTION financial_compute_guard_job_task();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER financial_compute_job_task_guard ON job_tasks;
DROP TRIGGER financial_compute_job_definition_guard ON jobs;
DROP FUNCTION financial_compute_guard_job_task();
DROP FUNCTION financial_compute_guard_job_definition();
DROP FUNCTION financial_compute_require_job_support(uuid);

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
