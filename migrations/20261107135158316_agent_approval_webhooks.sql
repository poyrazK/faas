-- migration-compat: expand

-- +goose Up
-- Replacing the CHECK locks app_webhooks briefly; the new constraint is added
-- NOT VALID and 20261107135200076 validates it, so the full check runs
-- without the exclusive lock.
SET LOCAL lock_timeout = '5s';
-- ADR-20261107140000 §4: account receivers may also subscribe to
-- agent.approval.requested, .approved and .denied. The marker makes replay a
-- no-op, as the agent run webhook migration does.
-- +goose StatementBegin
DO $do$
DECLARE definition text;
DECLARE widened text;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint
    WHERE conrelid = 'app_webhooks'::regclass AND conname = 'app_webhooks_scope_chk';
    IF position('agent.approval.requested' IN definition) = 0 THEN
        widened := replace(definition, '''agent_run.finished''::text',
            '''agent_run.finished''::text, ''agent.approval.requested''::text, ''agent.approval.approved''::text, ''agent.approval.denied''::text');
        widened := replace(widened, 'cardinality(event_filter) <= 6', 'cardinality(event_filter) <= 9');
        IF widened = definition OR position('cardinality(event_filter) <= 9' IN widened) = 0 THEN
            RAISE EXCEPTION 'app_webhooks_scope_chk has an unexpected shape: %', definition;
        END IF;
        ALTER TABLE app_webhooks DROP CONSTRAINT app_webhooks_scope_chk;
        EXECUTE 'ALTER TABLE app_webhooks ADD CONSTRAINT app_webhooks_scope_chk ' || widened || ' NOT VALID';
    END IF;
END;
$do$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: keep subscriptions and queued deliveries.
SELECT 1;
