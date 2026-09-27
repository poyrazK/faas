-- +goose Up
-- +goose StatementBegin
-- Guest child-process resource measurements are available only on supported
-- Linux one-shot runtime paths. Older rows and persistent worker pools retain
-- the zero/false defaults, so analytics can distinguish unknown from zero.
ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS guest_cpu_time_ms integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS guest_peak_rss_mb integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS guest_resource_usage_available boolean NOT NULL DEFAULT false;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'request_telemetry'::regclass
          AND conname = 'request_telemetry_guest_cpu_time_ms_check'
    ) THEN
        ALTER TABLE request_telemetry
            ADD CONSTRAINT request_telemetry_guest_cpu_time_ms_check
            CHECK (guest_cpu_time_ms BETWEEN 0 AND 86400000);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'request_telemetry'::regclass
          AND conname = 'request_telemetry_guest_peak_rss_mb_check'
    ) THEN
        ALTER TABLE request_telemetry
            ADD CONSTRAINT request_telemetry_guest_peak_rss_mb_check
            CHECK (guest_peak_rss_mb BETWEEN 0 AND 65536);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE request_telemetry
    DROP CONSTRAINT IF EXISTS request_telemetry_guest_peak_rss_mb_check,
    DROP CONSTRAINT IF EXISTS request_telemetry_guest_cpu_time_ms_check,
    DROP COLUMN IF EXISTS guest_resource_usage_available,
    DROP COLUMN IF EXISTS guest_peak_rss_mb,
    DROP COLUMN IF EXISTS guest_cpu_time_ms;
-- +goose StatementEnd
