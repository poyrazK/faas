-- +goose Up
-- +goose StatementBegin
-- The complete feature marker survives later migrations. Reconciliation must
-- preserve retained evidence and must not restore an earlier, narrower constraint.
DO $replay_guard$
BEGIN
 IF to_regclass('workflow_recovery_policy_executions_applied') IS NOT NULL AND to_regclass('workflow_recovery_policy_execution_charges_budget') IS NOT NULL THEN
  RETURN;
 END IF;
ALTER TABLE workflow_recovery_policies DROP CONSTRAINT workflow_recovery_policies_policy_check;
ALTER TABLE workflow_recovery_policies ADD CONSTRAINT workflow_recovery_policies_policy_check CHECK(jsonb_typeof(policy)='object' AND policy->>'mode' IN ('preview','automatic_retry'));
ALTER TABLE workflow_recovery_policies DROP CONSTRAINT workflow_recovery_policies_status_check;
ALTER TABLE workflow_recovery_policies ADD CONSTRAINT workflow_recovery_policies_status_check CHECK(status IN ('disabled','monitoring','review_limit','paused'));
ALTER TABLE workflow_recovery_policies ADD COLUMN next_execute_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE workflow_recovery_policies ADD COLUMN automatic_failures integer NOT NULL DEFAULT 0 CHECK(automatic_failures>=0);
ALTER TABLE workflow_recovery_policies ADD COLUMN automatic_paused_at timestamptz;
ALTER TABLE workflow_recovery_policies ADD COLUMN automatic_pause_reason text NOT NULL DEFAULT '';
CREATE TABLE workflow_recovery_policy_executions (
 action_id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES workflow_recovery_policies(app_id) ON DELETE CASCADE,
 review_id uuid NOT NULL REFERENCES workflow_recovery_policy_reviews(id) ON DELETE CASCADE,
 policy_version bigint NOT NULL CHECK(policy_version>0),
 position bigint NOT NULL CHECK(position>0),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','applied','suppressed')),
 reason text NOT NULL DEFAULT '',
 child_job_id uuid,
 applied_at timestamptz,
 result jsonb CHECK(result IS NULL OR (jsonb_typeof(result)='object' AND ((result->'complete'='true'::jsonb) IS TRUE))),
 CHECK((status='applied')=(child_job_id IS NOT NULL AND applied_at IS NOT NULL)),
 CHECK(result IS NULL OR status='applied')
);
CREATE TABLE workflow_recovery_policy_execution_charges (
 action_id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES workflow_recovery_policies(app_id) ON DELETE CASCADE,
 applied_at timestamptz NOT NULL
);
CREATE INDEX workflow_recovery_policy_execution_charges_budget ON workflow_recovery_policy_execution_charges(app_id,applied_at);
CREATE INDEX workflow_recovery_policy_executions_pending ON workflow_recovery_policy_executions(app_id,policy_version,review_id,position) WHERE status='pending';
CREATE INDEX workflow_recovery_policy_executions_applied ON workflow_recovery_policy_executions(app_id,applied_at) WHERE status='applied';
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_policy_paused');
END $$;
END
$replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policy_executions)
 OR EXISTS(SELECT 1 FROM workflow_recovery_policy_execution_charges)
 OR EXISTS(SELECT 1 FROM workflow_recovery_policies WHERE policy->>'mode'<>'preview' OR automatic_paused_at IS NOT NULL)
 OR EXISTS(SELECT 1 FROM app_webhook_event_outbox WHERE event='automation.recovery_policy_paused')
 OR EXISTS(SELECT 1 FROM app_webhook_deliveries WHERE event='automation.recovery_policy_paused') THEN
 RAISE EXCEPTION 'cannot downgrade while automatic recovery policy evidence is retained'; END IF;
END $$;
DROP TABLE workflow_recovery_policy_execution_charges;
DROP TABLE workflow_recovery_policy_executions;
ALTER TABLE workflow_recovery_policies DROP COLUMN next_execute_at;
ALTER TABLE workflow_recovery_policies DROP COLUMN automatic_failures;
ALTER TABLE workflow_recovery_policies DROP COLUMN automatic_paused_at;
ALTER TABLE workflow_recovery_policies DROP COLUMN automatic_pause_reason;
ALTER TABLE workflow_recovery_policies DROP CONSTRAINT workflow_recovery_policies_status_check;
ALTER TABLE workflow_recovery_policies ADD CONSTRAINT workflow_recovery_policies_status_check CHECK(status IN ('disabled','monitoring','review_limit'));
ALTER TABLE workflow_recovery_policies DROP CONSTRAINT workflow_recovery_policies_policy_check;
ALTER TABLE workflow_recovery_policies ADD CONSTRAINT workflow_recovery_policies_policy_check CHECK(jsonb_typeof(policy)='object' AND policy->>'mode'='preview');
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) AND event<>%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_policy_paused');
END $$;
-- +goose StatementEnd
