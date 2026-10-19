-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1641. A job is not an app. Retain claims even when task projections
-- advance; native cancellation and lost-response recovery need the old fence.
CREATE TABLE IF NOT EXISTS financial_compute_job_books (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 job_id uuid NOT NULL,
 period_start timestamptz NOT NULL,
 period_end timestamptz NOT NULL CHECK (period_end>period_start),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
 CHECK (COALESCE((payload->>'committed')::bigint>=0 AND (payload->>'reserved')::bigint>=0
  AND (payload->>'committed')::bigint+(payload->>'reserved')::bigint<=9007199254740991,false)),
 PRIMARY KEY(account_id,job_id,period_start)
);
ALTER TABLE financial_compute_leases ALTER COLUMN app_id DROP NOT NULL;
ALTER TABLE financial_compute_leases ADD COLUMN IF NOT EXISTS job_id uuid, ADD COLUMN IF NOT EXISTS job_run_id uuid, ADD COLUMN IF NOT EXISTS job_task_index integer;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_leases'::regclass AND conname='financial_compute_workload_identity') THEN
  ALTER TABLE financial_compute_leases ADD CONSTRAINT financial_compute_workload_identity CHECK (
 (app_id IS NOT NULL AND job_id IS NULL AND job_run_id IS NULL AND job_task_index IS NULL)
 OR (app_id IS NULL AND job_id IS NOT NULL AND job_run_id IS NOT NULL AND job_task_index IS NOT NULL AND job_task_index>=0));
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS financial_compute_job ON financial_compute_leases(account_id,job_id,period_start);
ALTER TABLE financial_compute_owners ADD COLUMN IF NOT EXISTS job_compute_incarnation uuid;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_owners'::regclass AND conname='financial_compute_job_owner_incarnation') THEN
  ALTER TABLE financial_compute_owners ADD CONSTRAINT financial_compute_job_owner_incarnation
 CHECK(job_compute_incarnation IS NULL OR job_compute_incarnation=incarnation);
 END IF;
END $$;

CREATE TABLE IF NOT EXISTS financial_compute_job_attempts (
 instance_id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 job_id uuid NOT NULL,
 run_id uuid NOT NULL,
 task_index integer NOT NULL CHECK(task_index>=0),
 lease_token uuid NOT NULL,
 node_id uuid NOT NULL,
 incarnation uuid,
 ram_mb integer NOT NULL CHECK(ram_mb>0),
 claimed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(run_id,task_index,lease_token)
);
INSERT INTO financial_compute_job_attempts(instance_id,account_id,job_id,run_id,task_index,lease_token,node_id,incarnation,ram_mb)
 SELECT i.id,r.account_id,r.job_id,t.run_id,t.task_index,t.lease_token,i.node_id,n.vmmd_incarnation,i.ram_mb
 FROM job_tasks t JOIN job_runs r ON r.id=t.run_id
 JOIN jobs j ON j.id=r.job_id AND j.account_id=r.account_id
 JOIN instances i ON i.id=t.instance_id AND i.job_id=j.id LEFT JOIN compute_nodes n ON n.id=i.node_id
 WHERE t.status='claimed' AND t.lease_token IS NOT NULL AND i.node_id IS NOT NULL ON CONFLICT DO NOTHING;

-- Queue and definition writes are safe, but must still serialize with policy
-- activation. Claim admission below fences every legacy physical producer.
CREATE OR REPLACE FUNCTION financial_compute_require_job_support(target_account uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM accounts WHERE id=target_account FOR UPDATE;
END
$$;

CREATE OR REPLACE FUNCTION financial_compute_guard_job_task() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE target_account uuid; target_job uuid; target_node uuid; target_incarnation uuid; target_ram integer;
BEGIN
 IF NEW.status NOT IN ('queued','claimed') THEN RETURN NEW; END IF;
 SELECT account_id,job_id INTO target_account,target_job FROM job_runs WHERE id=NEW.run_id;
 IF target_account IS NULL THEN RETURN NEW; END IF;
 -- KEY SHARE conflicts with the FOR UPDATE that every policy and compute
 -- transaction takes, but not with other task writers: a fan-out run's
 -- claims serialize with activation, not with each other.
 PERFORM 1 FROM accounts WHERE id=target_account FOR KEY SHARE;
 IF NEW.status!='claimed' THEN RETURN NEW; END IF;
 SELECT i.node_id,n.vmmd_incarnation,i.ram_mb INTO target_node,target_incarnation,target_ram FROM instances i
 JOIN jobs j ON j.id=i.job_id AND j.account_id=target_account
 LEFT JOIN compute_nodes n ON n.id=i.node_id
 WHERE i.id=NEW.instance_id AND i.job_id=target_job AND i.kind='job_task';
 IF EXISTS(SELECT 1 FROM financial_budget_policies p WHERE p.account_id=target_account
  AND p.deleted_at IS NULL AND p.spec->>'enabled'='true' AND p.spec->>'mode'='strict'
  AND (p.spec->'scope'->>'kind'='account' OR
   (p.spec->'scope'->>'kind'='job' AND p.spec->'scope'->>'id'=target_job::text))) THEN
  IF NOT EXISTS(SELECT 1 FROM financial_compute_owners o JOIN compute_nodes n ON n.id=o.node_id
    WHERE n.id=target_node AND n.active AND o.incarnation=n.vmmd_incarnation
    AND o.job_compute_incarnation=o.incarnation AND o.account_compute_incarnation=o.incarnation) THEN
   RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='financial_compute_job_owner_capable',
    MESSAGE='strict job compute requires a current capable native owner';
  END IF;
 END IF;
 IF target_node IS NULL OR target_ram IS NULL OR NEW.lease_token IS NULL THEN
  RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='financial_compute_job_claim_identity',
   MESSAGE='job claim requires a bound physical instance and lease';
 END IF;
 INSERT INTO financial_compute_job_attempts(instance_id,account_id,job_id,run_id,task_index,lease_token,node_id,incarnation,ram_mb)
 VALUES(NEW.instance_id,target_account,target_job,NEW.run_id,NEW.task_index,NEW.lease_token,target_node,target_incarnation,target_ram)
 ON CONFLICT(instance_id) DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM financial_compute_job_attempts a WHERE a.instance_id=NEW.instance_id
  AND a.account_id=target_account AND a.job_id=target_job AND a.run_id=NEW.run_id
  AND a.task_index=NEW.task_index AND a.lease_token=NEW.lease_token
  AND a.node_id=target_node AND a.incarnation IS NOT DISTINCT FROM target_incarnation AND a.ram_mb=target_ram) THEN
  RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='financial_compute_job_claim_identity',
   MESSAGE='job physical attempt identity cannot be reused';
 END IF;
 RETURN NEW;
END
$$;
DROP TRIGGER IF EXISTS financial_compute_job_task_guard ON job_tasks;
CREATE OR REPLACE TRIGGER financial_compute_job_task_guard BEFORE INSERT OR UPDATE OF status,run_id,instance_id,lease_token ON job_tasks
 FOR EACH ROW EXECUTE FUNCTION financial_compute_guard_job_task();
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback that would erase physical job funding or its fences.
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'ADR-1641 compute authority migration requires a coordinated forward repair';
END $$;
-- +goose StatementEnd

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
