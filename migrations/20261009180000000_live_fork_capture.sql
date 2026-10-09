-- filename: 20261009180000000_live_fork_capture.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-732 live forks. A live fork captures the app's newest running
-- instance now (in place, like a manual crash capture) and restores that
-- capture, instead of the deployment's last snapshot. The capture is a
-- crash_captures row with trigger 'live_fork', written by apid together
-- with the fork that is pinned to it; it is kept only as long as the
-- longest fork can live.
ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_trigger_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_trigger_chk
    CHECK (trigger IN ('http_5xx', 'manual', 'sdk', 'live_fork'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM crash_captures WHERE trigger = 'live_fork';
ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_trigger_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_trigger_chk
    CHECK (trigger IN ('http_5xx', 'manual', 'sdk'));
-- +goose StatementEnd
