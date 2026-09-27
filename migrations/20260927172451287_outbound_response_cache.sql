-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS response_cache_ttl_seconds integer NOT NULL DEFAULT 0;

-- Keep the volatile cache short-lived even if a legacy writer bypasses API
-- validation or a future plan accidentally raises its configured ceiling.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_response_cache_ttl_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_response_cache_ttl_chk
                CHECK (response_cache_ttl_seconds BETWEEN 0 AND 300);
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_response_cache_ttl_chk,
    DROP COLUMN IF EXISTS response_cache_ttl_seconds;
