-- filename: 20260922165732124_service_rollout_handoff_state.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-208 follow-up: the deployment row is already the durable recovery
-- record for a service rollout. Keep the gateway/drain handoff alongside it
-- so a replacement schedd can resume the exact action and operators can see
-- why a rollout is still rolling_out without reconstructing scheduler logs.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS service_rollout_handoff jsonb NOT NULL DEFAULT '{}'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conrelid = 'deployments'::regclass
          AND conname = 'deployments_service_rollout_handoff_object_chk'
    ) THEN
        ALTER TABLE deployments
            ADD CONSTRAINT deployments_service_rollout_handoff_object_chk
            CHECK (jsonb_typeof(service_rollout_handoff) = 'object');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conrelid = 'deployments'::regclass
          AND conname = 'deployments_service_rollout_handoff_size_chk'
    ) THEN
        ALTER TABLE deployments
            ADD CONSTRAINT deployments_service_rollout_handoff_size_chk
            CHECK (octet_length(service_rollout_handoff::text) <= 16384);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conrelid = 'deployments'::regclass
          AND conname = 'deployments_service_rollout_handoff_state_chk'
    ) THEN
        ALTER TABLE deployments
            ADD CONSTRAINT deployments_service_rollout_handoff_state_chk
            CHECK (
                service_rollout_handoff = '{}'::jsonb
                OR (
                    service_rollout_handoff->>'action' IN ('promote', 'abort')
                    AND service_rollout_handoff->>'phase' IN ('pending', 'routing', 'draining', 'complete')
                    AND coalesce((service_rollout_handoff->>'retry_count')::integer, 0) >= 0
                )
            );
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_service_rollout_handoff_state_chk,
    DROP CONSTRAINT IF EXISTS deployments_service_rollout_handoff_size_chk,
    DROP CONSTRAINT IF EXISTS deployments_service_rollout_handoff_object_chk,
    DROP COLUMN IF EXISTS service_rollout_handoff;
-- +goose StatementEnd
