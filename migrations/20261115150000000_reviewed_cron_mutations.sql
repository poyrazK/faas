-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE cron_mutation_receipts (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    request_id uuid NOT NULL,
    cron_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('accepted','partial','completed','conflict')),
    record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    retained_until timestamptz NOT NULL,
    PRIMARY KEY (app_id, request_id),
    CHECK (record->'receipt'->>'status' = status),
    CHECK (record->'receipt'->>'phase' IN ('cancel_pending','detach_invocations','detach_tasks','delete','complete'))
);
CREATE INDEX cron_mutation_receipts_target ON cron_mutation_receipts(app_id, cron_id);
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON cron_mutation_receipts TO faas_runtime;
    END IF;
END $$;
-- +goose StatementEnd
-- Receipts deliberately outlive cron deletion. Current app ownership is checked
-- on every read/write; no private proof or cron configuration is persisted here.

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE cron_mutation_receipts;
