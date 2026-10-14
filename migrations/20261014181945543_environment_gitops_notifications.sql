-- +goose Up
ALTER TABLE environment_git_sources ADD COLUMN IF NOT EXISTS notification_state jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(notification_state) = 'object' AND octet_length(notification_state::text) <= 2048);

-- Preserve the complete platform-tenant vocabulary when extending the account
-- filter. The marker makes replay a no-op rather than appending a second item.
-- +goose StatementBegin
DO $do$
DECLARE definition text;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint
    WHERE conrelid = 'app_webhooks'::regclass AND conname = 'app_webhooks_scope_chk';
    IF position('environment.gitops.changed' IN definition) = 0 THEN
        definition := replace(definition, '''rollout.aborted''::text', '''rollout.aborted''::text, ''environment.gitops.changed''::text');
        definition := replace(definition, 'cardinality(event_filter) <= 4', 'cardinality(event_filter) <= 5');
        ALTER TABLE app_webhooks DROP CONSTRAINT app_webhooks_scope_chk;
        EXECUTE 'ALTER TABLE app_webhooks ADD CONSTRAINT app_webhooks_scope_chk ' || definition;
    END IF;
END;
$do$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: retain notification baselines and queued deliveries.
SELECT 1;
