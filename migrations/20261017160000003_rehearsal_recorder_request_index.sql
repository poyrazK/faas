-- +goose Up
CREATE INDEX scenario_recorder_requests_session_status ON scenario_recorder_requests(session_id,status);

-- +goose Down
DROP INDEX scenario_recorder_requests_session_status;
