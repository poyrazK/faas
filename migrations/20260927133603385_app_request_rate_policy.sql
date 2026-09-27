-- +goose Up
-- +goose StatementBegin

-- App-wide edge rate limits are runtime policy. Nullable values inherit the
-- plan defaults; zero is accepted by PATCH but normalized to NULL by the
-- store so reads remain unambiguous.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS request_rate_limit_rps integer,
    ADD COLUMN IF NOT EXISTS request_rate_limit_burst integer;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'apps_request_rate_limit_rps_positive'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_request_rate_limit_rps_positive
            CHECK (request_rate_limit_rps IS NULL OR request_rate_limit_rps > 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'apps_request_rate_limit_burst_positive'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_request_rate_limit_burst_positive
            CHECK (request_rate_limit_burst IS NULL OR request_rate_limit_burst > 0);
    END IF;
END
$$;

COMMENT ON COLUMN apps.request_rate_limit_rps IS
    'Optional app-wide edge token-bucket refill override. NULL inherits the account plan.';
COMMENT ON COLUMN apps.request_rate_limit_burst IS
    'Optional app-wide edge token-bucket burst override. NULL inherits the account plan.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_request_rate_limit_burst_positive;
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_request_rate_limit_rps_positive;
ALTER TABLE apps DROP COLUMN IF EXISTS request_rate_limit_burst;
ALTER TABLE apps DROP COLUMN IF EXISTS request_rate_limit_rps;
-- +goose StatementEnd
