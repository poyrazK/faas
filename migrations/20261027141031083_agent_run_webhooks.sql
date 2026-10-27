-- filename: 20261027141031083_agent_run_webhooks.sql
-- migration-compat: expand

-- +goose Up
-- Replacing the CHECK locks app_webhooks briefly; the new constraint is added
-- NOT VALID and 20261027141127468 validates it, so the full check runs
-- without the exclusive lock.
SET LOCAL lock_timeout = '5s';
-- ADR-20261027090010 §8 and ADR-224: account-scoped receivers may also subscribe to
-- agent_run.finished, delivered through the same signed ledger. The marker
-- makes replay a no-op, as the environment GitOps migration does.
-- +goose StatementBegin
DO $do$
DECLARE definition text;
DECLARE widened text;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint
    WHERE conrelid = 'app_webhooks'::regclass AND conname = 'app_webhooks_scope_chk';
    IF position('agent_run.finished' IN definition) = 0 THEN
        widened := replace(definition, '''environment.gitops.changed''::text', '''environment.gitops.changed''::text, ''agent_run.finished''::text');
        widened := replace(widened, 'cardinality(event_filter) <= 5', 'cardinality(event_filter) <= 6');
        IF widened = definition OR position('cardinality(event_filter) <= 6' IN widened) = 0 THEN
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
