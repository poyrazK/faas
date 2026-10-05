-- +goose Up
ALTER TABLE workflow_steps ADD COLUMN IF NOT EXISTS outbound_attempt_token uuid;
ALTER TABLE workflow_steps
 DROP CONSTRAINT IF EXISTS workflow_steps_outbound_attempt_token_check,
 ADD CONSTRAINT workflow_steps_outbound_attempt_token_check CHECK (
   outbound_attempt_token IS NULL OR outbound_attempt_token <> '00000000-0000-0000-0000-000000000000'
 );

-- +goose Down
ALTER TABLE workflow_steps DROP CONSTRAINT IF EXISTS workflow_steps_outbound_attempt_token_check;
ALTER TABLE workflow_steps DROP COLUMN IF EXISTS outbound_attempt_token;
