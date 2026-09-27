-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS circuit_breaker_failure_threshold integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS circuit_breaker_open_seconds integer NOT NULL DEFAULT 0;

-- Keep both disabled together or require bounded values for an active breaker.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_circuit_breaker_policy_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_circuit_breaker_policy_chk
                CHECK (
                    (circuit_breaker_failure_threshold = 0 AND circuit_breaker_open_seconds = 0)
                    OR (circuit_breaker_failure_threshold BETWEEN 1 AND 20
                        AND circuit_breaker_open_seconds BETWEEN 1 AND 300)
                );
    END IF;
END$$;
-- +goose StatementEnd

ALTER TABLE outbound_admission_state
    ADD COLUMN IF NOT EXISTS circuit_failure_count integer NOT NULL DEFAULT 0 CHECK (circuit_failure_count >= 0),
    ADD COLUMN IF NOT EXISTS circuit_open_until timestamptz,
    ADD COLUMN IF NOT EXISTS circuit_probe_lease_id uuid REFERENCES outbound_admission_leases(lease_id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS circuit_policy_failure_threshold integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS circuit_policy_open_seconds integer NOT NULL DEFAULT 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_admission_state_circuit_policy_chk'
          AND conrelid = 'outbound_admission_state'::regclass
    ) THEN
        ALTER TABLE outbound_admission_state
            ADD CONSTRAINT outbound_admission_state_circuit_policy_chk
                CHECK (
                    (circuit_policy_failure_threshold = 0 AND circuit_policy_open_seconds = 0)
                    OR (circuit_policy_failure_threshold BETWEEN 1 AND 20
                        AND circuit_policy_open_seconds BETWEEN 1 AND 300)
                );
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE outbound_admission_state
    DROP CONSTRAINT IF EXISTS outbound_admission_state_circuit_policy_chk,
    DROP COLUMN IF EXISTS circuit_policy_open_seconds,
    DROP COLUMN IF EXISTS circuit_policy_failure_threshold,
    DROP COLUMN IF EXISTS circuit_probe_lease_id,
    DROP COLUMN IF EXISTS circuit_open_until,
    DROP COLUMN IF EXISTS circuit_failure_count;

ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_circuit_breaker_policy_chk,
    DROP COLUMN IF EXISTS circuit_breaker_open_seconds,
    DROP COLUMN IF EXISTS circuit_breaker_failure_threshold;
