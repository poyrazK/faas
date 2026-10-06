-- +goose Up
-- Compact JSON bytes preserve the exact base64/metadata output charge.
ALTER TABLE executions ADD COLUMN IF NOT EXISTS artifacts bytea NOT NULL DEFAULT ''::bytea;
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_artifacts_check') THEN
  ALTER TABLE executions ADD CONSTRAINT executions_artifacts_check CHECK (
   octet_length(artifacts) = 0 OR (status = 'succeeded' AND octet_length(artifacts) <= max_output_bytes)
  );
 END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_output_budget_check;
ALTER TABLE executions ADD CONSTRAINT executions_output_budget_check CHECK (
 octet_length(stdout) + octet_length(stderr) + result_bytes + octet_length(artifacts) <= max_output_bytes
);

-- +goose Down
-- Dropping artifacts also removes their associated budget constraint.
ALTER TABLE executions DROP CONSTRAINT executions_output_budget_check;
ALTER TABLE executions DROP COLUMN artifacts;
ALTER TABLE executions ADD CONSTRAINT executions_output_budget_check CHECK (
 octet_length(stdout) + octet_length(stderr) + result_bytes <= max_output_bytes
);
