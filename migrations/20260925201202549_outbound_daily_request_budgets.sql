-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN daily_request_limit bigint
        CONSTRAINT outbound_integrations_daily_request_limit_chk
        CHECK (daily_request_limit IS NULL OR daily_request_limit BETWEEN 1 AND 100000000);

ALTER TABLE outbound_admission_state
    ADD COLUMN daily_usage_date date NOT NULL DEFAULT ((now() AT TIME ZONE 'UTC')::date),
    ADD COLUMN daily_request_count bigint NOT NULL DEFAULT 0
        CONSTRAINT outbound_admission_state_daily_request_count_chk
        CHECK (daily_request_count >= 0);

-- +goose Down
ALTER TABLE outbound_admission_state
    DROP CONSTRAINT outbound_admission_state_daily_request_count_chk,
    DROP COLUMN daily_request_count,
    DROP COLUMN daily_usage_date;
ALTER TABLE outbound_integrations
    DROP CONSTRAINT outbound_integrations_daily_request_limit_chk,
    DROP COLUMN daily_request_limit;
