-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS max_retries integer NOT NULL DEFAULT 0;

-- A hard storage bound complements API validation and protects older/operator
-- writers from persisting an unbounded retry multiplier.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_max_retries_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_max_retries_chk
                CHECK (max_retries BETWEEN 0 AND 2);
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE outbound_integrations
    DROP CONSTRAINT outbound_integrations_max_retries_chk,
    DROP COLUMN max_retries;
