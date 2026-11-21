-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE execution_run_budgets (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('customer','task')),
 ref text NOT NULL CHECK (ref ~ '^[A-Za-z0-9_.:-]{1,96}$'),
 revision bigint NOT NULL CHECK (revision>0),
 enabled boolean NOT NULL,
 max_concurrent bigint CHECK (max_concurrent BETWEEN 0 AND 32),
 max_invocations bigint CHECK (max_invocations BETWEEN 0 AND 1000000000),
 max_compute_mb_ms bigint CHECK (max_compute_mb_ms BETWEEN 0 AND 1000000000000000),
 PRIMARY KEY(account_id,kind,ref)
);
CREATE TABLE execution_run_bindings (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 subject_kind text NOT NULL CHECK (subject_kind IN ('api_key','agent_endpoint')),
 subject_id uuid NOT NULL,
 customer_ref text NOT NULL CHECK (customer_ref ~ '^[A-Za-z0-9_.:-]{1,96}$'),
 agent_ref text NOT NULL CHECK (agent_ref='' OR agent_ref ~ '^[A-Za-z0-9_.:-]{1,96}$'),
 revision bigint NOT NULL CHECK (revision>0),
 enabled boolean NOT NULL DEFAULT true,
 PRIMARY KEY(account_id,subject_kind,subject_id)
);
CREATE TABLE execution_run_reservations (
 execution_id uuid PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 customer_ref text NOT NULL CHECK (customer_ref='' OR customer_ref ~ '^[A-Za-z0-9_.:-]{1,96}$'),
 agent_ref text NOT NULL CHECK (agent_ref='' OR agent_ref ~ '^[A-Za-z0-9_.:-]{1,96}$'),
 task_name text NOT NULL CHECK (task_name='' OR task_name ~ '^[a-z][a-z0-9-]{0,62}$'),
 reserved_mb_ms bigint NOT NULL CHECK (reserved_mb_ms>=0),
 admitted_at timestamptz NOT NULL
);
CREATE INDEX execution_run_customer_usage ON execution_run_reservations(account_id,customer_ref,admitted_at);
CREATE INDEX execution_run_task_usage ON execution_run_reservations(account_id,task_name,admitted_at);
CREATE TABLE execution_run_budget_alerts (
 event_id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('customer','task')),
 ref text NOT NULL CHECK (ref ~ '^[A-Za-z0-9_.:-]{1,96}$'),
 day date NOT NULL,
 metric text NOT NULL CHECK (metric IN ('concurrent','invocations','compute_mb_ms')),
 threshold_percent integer NOT NULL CHECK (threshold_percent IN (80,100)),
 maximum bigint NOT NULL CHECK (maximum>0),
 observed bigint NOT NULL CHECK (observed>=0),
 created_at timestamptz NOT NULL,
 UNIQUE(account_id,kind,ref,day,metric,threshold_percent)
);
-- N-1 readers/terminal writers remain compatible. An old admission writer
-- cannot bypass controls once any policy has been configured for its account.
-- +goose StatementBegin
CREATE FUNCTION execution_run_controls_writer_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('faas.run_controls_protocol',true) IS DISTINCT FROM '1'
 AND EXISTS (SELECT 1 FROM execution_run_budgets WHERE account_id=NEW.account_id) THEN
  RAISE EXCEPTION 'Run controls require a compatible admission writer' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER execution_run_controls_writer_fence BEFORE INSERT ON executions FOR EACH ROW EXECUTE FUNCTION execution_run_controls_writer_fence();
-- +goose StatementBegin
DO $do$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint WHERE conrelid='app_webhooks'::regclass AND conname='app_webhooks_scope_chk';
 definition := replace(definition, '''execution.finished''::text', '''execution.finished''::text, ''execution.budget.threshold''::text');
 definition := replace(definition, 'cardinality(event_filter) <= 10', 'cardinality(event_filter) <= 11');
 IF position('execution.budget.threshold' IN definition)=0 THEN RAISE EXCEPTION 'unexpected account receiver constraint'; END IF;
 EXECUTE 'ALTER TABLE app_webhooks ADD CONSTRAINT app_webhooks_run_budget_scope_chk ' || definition || ' NOT VALID';
END;
$do$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
