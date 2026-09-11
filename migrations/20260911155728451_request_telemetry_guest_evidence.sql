-- +goose Up
-- +goose StatementBegin
-- Guest execution evidence is a bounded, platform-owned signal from the
-- runtime runner. Defaults keep rolling upgrades compatible with old runners.
ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS guest_duration_ms integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS guest_runtime text NOT NULL DEFAULT '__unknown__',
    ADD COLUMN IF NOT EXISTS guest_outcome text NOT NULL DEFAULT 'missing',
    ADD COLUMN IF NOT EXISTS guest_error_class text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'request_telemetry'::regclass
                     AND conname = 'request_telemetry_guest_duration_ms_check') THEN
        ALTER TABLE request_telemetry
            ADD CONSTRAINT request_telemetry_guest_duration_ms_check
            CHECK (guest_duration_ms BETWEEN 0 AND 86400000);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'request_telemetry'::regclass
                     AND conname = 'request_telemetry_guest_runtime_check') THEN
        ALTER TABLE request_telemetry
            ADD CONSTRAINT request_telemetry_guest_runtime_check
            CHECK (guest_runtime = ANY (ARRAY['node22','node24','python312','python313','go124','__unknown__']));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'request_telemetry'::regclass
                     AND conname = 'request_telemetry_guest_outcome_check') THEN
        ALTER TABLE request_telemetry
            ADD CONSTRAINT request_telemetry_guest_outcome_check
            CHECK (guest_outcome = ANY (ARRAY['ok','http_error','handler_error','timeout','canceled','missing']));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'request_telemetry'::regclass
                     AND conname = 'request_telemetry_guest_error_class_check') THEN
        ALTER TABLE request_telemetry
            ADD CONSTRAINT request_telemetry_guest_error_class_check
            CHECK (guest_error_class = ANY (ARRAY['','http_5xx','handler_exec','handler_protocol','timeout','canceled']));
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE request_telemetry
    DROP CONSTRAINT IF EXISTS request_telemetry_guest_error_class_check,
    DROP CONSTRAINT IF EXISTS request_telemetry_guest_outcome_check,
    DROP CONSTRAINT IF EXISTS request_telemetry_guest_runtime_check,
    DROP CONSTRAINT IF EXISTS request_telemetry_guest_duration_ms_check,
    DROP COLUMN IF EXISTS guest_error_class,
    DROP COLUMN IF EXISTS guest_outcome,
    DROP COLUMN IF EXISTS guest_runtime,
    DROP COLUMN IF EXISTS guest_duration_ms;
-- +goose StatementEnd
