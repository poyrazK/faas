-- filename: 20261021171858895_outbound_ai_quality_drift_webhook.sql
-- ADR 20261021171712: drift alerts notify bound apps with the
-- outbound.ai_quality.drift webhook. The outbox's event check is rebuilt
-- with it NOT VALID; 20261021171859791 validates it. Replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
    -- Preserve the webhook vocabulary installed by earlier producers.
    SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
     WHERE conrelid = 'app_webhook_event_outbox'::regclass AND conname = 'app_webhook_event_outbox_event_chk';
    IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
    IF position('outbound.ai_quality.drift' in definition) = 0 THEN
        definition := regexp_replace(definition, ' NOT VALID$', '');
        ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
        EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L)) NOT VALID',
                       substring(definition from 8 for length(definition) - 8), 'outbound.ai_quality.drift');
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Keep the webhook vocabulary so queued deliveries remain dispatchable.
SELECT 1;
-- +goose StatementEnd
