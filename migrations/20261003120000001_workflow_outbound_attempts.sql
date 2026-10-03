-- +goose Up
ALTER TABLE workflow_steps ADD COLUMN outbound_attempt_token uuid
 CHECK (outbound_attempt_token IS NULL OR outbound_attempt_token <> '00000000-0000-0000-0000-000000000000');

-- +goose Down
ALTER TABLE workflow_steps DROP COLUMN outbound_attempt_token;
