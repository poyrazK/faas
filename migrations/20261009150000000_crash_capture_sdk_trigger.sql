-- filename: 20261009150000000_crash_capture_sdk_trigger.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-733 SDK trigger. An app asks for a crash capture of its own instance
-- from inside its error handler, through the guest metadata endpoint; vmmd
-- writes the request row with the instance identity from its live map (the
-- vsock listener, not the caller, names the instance). `reason` is the
-- app's free-text label for the capture; `route` stays the failing request
-- path when the app passes one.
ALTER TABLE crash_captures
    ADD COLUMN IF NOT EXISTS reason text NOT NULL DEFAULT '';

ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_trigger_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_trigger_chk
    CHECK (trigger IN ('http_5xx', 'manual', 'sdk'));

ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_reason_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_reason_chk
    CHECK (octet_length(reason) <= 256 AND (trigger = 'sdk' OR reason = ''));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM crash_captures WHERE trigger = 'sdk';
ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_reason_chk;
ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_trigger_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_trigger_chk
    CHECK (trigger IN ('http_5xx', 'manual'));
ALTER TABLE crash_captures DROP COLUMN IF EXISTS reason;
-- +goose StatementEnd
