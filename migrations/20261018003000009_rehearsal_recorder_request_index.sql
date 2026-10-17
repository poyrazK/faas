-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE INDEX scenario_recorder_requests_session_status ON scenario_recorder_requests(session_id,status);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP INDEX scenario_recorder_requests_session_status;
