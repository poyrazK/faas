-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS daily_request_limit bigint;

ALTER TABLE outbound_admission_state
    ADD COLUMN IF NOT EXISTS daily_usage_date date NOT NULL DEFAULT ((now() AT TIME ZONE 'UTC')::date),
    ADD COLUMN IF NOT EXISTS daily_request_count bigint NOT NULL DEFAULT 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_daily_request_limit_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_daily_request_limit_chk
                CHECK (daily_request_limit IS NULL OR daily_request_limit BETWEEN 1 AND 100000000);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_admission_state_daily_request_count_chk'
          AND conrelid = 'outbound_admission_state'::regclass
    ) THEN
        ALTER TABLE outbound_admission_state
            ADD CONSTRAINT outbound_admission_state_daily_request_count_chk
                CHECK (daily_request_count >= 0);
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE outbound_admission_state
    DROP CONSTRAINT outbound_admission_state_daily_request_count_chk,
    DROP COLUMN daily_request_count,
    DROP COLUMN daily_usage_date;
ALTER TABLE outbound_integrations
    DROP CONSTRAINT outbound_integrations_daily_request_limit_chk,
    DROP COLUMN daily_request_limit;
