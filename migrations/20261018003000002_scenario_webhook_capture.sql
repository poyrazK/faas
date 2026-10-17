-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_test_action_policies') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_test_action_policies_run_idx') AND relkind IN ('i','I'))
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('app_webhook_deliveries') AND tgname='scenario_webhook_delivery_fence' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('exclusive_work_effects') AND attname='rehearsal_run_id' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('workflow_operation_effects') AND attname='rehearsal_run_id' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_webhook_delivery_fence()') AND md5(prosrc)=ANY(ARRAY['53e339f5ad976a87256eec38042916a5'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_test_action_policies') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_test_action_policies_run_idx') AND relkind IN ('i','I'))
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('app_webhook_deliveries') AND tgname='scenario_webhook_delivery_fence' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('exclusive_work_effects') AND attname='rehearsal_run_id' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('workflow_operation_effects') AND attname='rehearsal_run_id' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_webhook_delivery_fence()') AND md5(prosrc)=ANY(ARRAY['53e339f5ad976a87256eec38042916a5'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 2; inspect recovery before continuing';
  END IF;

CREATE TABLE scenario_test_action_policies (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    run_id text NOT NULL CHECK (run_id ~ '^[0-9a-f]{32}$'),
    webhook_mode text NOT NULL CHECK (webhook_mode = 'capture'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX scenario_test_action_policies_run_idx ON scenario_test_action_policies(account_id, run_id);

ALTER TABLE exclusive_work_effects ADD COLUMN rehearsal_run_id text NOT NULL DEFAULT ''
    CHECK (rehearsal_run_id = '' OR (webhook_id IS NOT NULL AND rehearsal_run_id ~ '^[0-9a-f]{32}$'));
ALTER TABLE workflow_operation_effects ADD COLUMN rehearsal_run_id text NOT NULL DEFAULT ''
    CHECK (rehearsal_run_id = '' OR rehearsal_run_id ~ '^[0-9a-f]{32}$');

-- This fence also protects mixed-version dispatchers: ordinary events and
-- manual retries cannot create a runnable row for an enrolled app. Policies
-- outlive namespace deletion and are removed only with the app/account.
CREATE FUNCTION scenario_webhook_delivery_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Serialize new deliveries with the registration transaction. A registration
    -- takes FOR UPDATE and refuses any app that already has work or deliveries.
    PERFORM id FROM apps
    WHERE id = NEW.app_id OR id = (SELECT app_id FROM app_webhooks WHERE id = NEW.webhook_id)
    ORDER BY id FOR KEY SHARE;
    IF EXISTS (
        SELECT 1 FROM scenario_test_action_policies p
        WHERE p.app_id = NEW.app_id
           OR p.app_id = (SELECT app_id FROM app_webhooks WHERE id = NEW.webhook_id)
    ) AND NEW.status IN ('pending', 'in_flight', 'failed') THEN
        NEW.status := 'dead';
        NEW.last_error := 'rehearsal: unsupported webhook delivery suppressed';
        NEW.updated_at := clock_timestamp();
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER scenario_webhook_delivery_fence
BEFORE INSERT OR UPDATE ON app_webhook_deliveries
FOR EACH ROW EXECUTE FUNCTION scenario_webhook_delivery_fence();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Capture authority must never be silently removed by a routine rollback while
-- its retained effect records or suppressed deliveries still exist.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM scenario_test_action_policies) THEN
        RAISE EXCEPTION 'cannot remove scenario webhook capture policies while enrolled apps remain';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_webhook_delivery_fence ON app_webhook_deliveries;
DROP FUNCTION scenario_webhook_delivery_fence();
ALTER TABLE workflow_operation_effects DROP COLUMN rehearsal_run_id;
ALTER TABLE exclusive_work_effects DROP COLUMN rehearsal_run_id;
DROP TABLE scenario_test_action_policies;
