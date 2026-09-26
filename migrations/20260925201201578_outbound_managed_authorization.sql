-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS provider_auth_mode text NOT NULL DEFAULT 'application';

-- A schema can contain the column while its goose ledger is behind (for
-- example, after a partially applied deploy). Guard the separate constraint
-- too, since ADD COLUMN IF NOT EXISTS skips its inline definition when the
-- column already exists.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_provider_auth_mode_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_provider_auth_mode_chk
                CHECK (provider_auth_mode IN ('application', 'managed'));
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE outbound_integrations DROP COLUMN provider_auth_mode;
