-- filename: 20261121140659106_execution_files.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE executions ADD COLUMN IF NOT EXISTS max_artifact_bytes bigint;
ALTER TABLE executions ADD COLUMN IF NOT EXISTS input_file_bytes bigint;
-- +goose StatementBegin
DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'executions'::regclass AND conname = 'executions_file_budget_check') THEN
ALTER TABLE executions ADD CONSTRAINT executions_file_budget_check CHECK ((max_artifact_bytes IS NULL OR max_artifact_bytes BETWEEN 0 AND 67108864) AND (input_file_bytes IS NULL OR input_file_bytes BETWEEN 0 AND 67108864)) NOT VALID;
END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS execution_files (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    principal_id uuid,
    execution_id uuid,
    name text NOT NULL DEFAULT '',
    kind text NOT NULL CHECK (kind IN ('upload','output')),
    status text NOT NULL CHECK (status IN ('uploading','pending','ready','failed','deleting')),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 0 AND 67108864),
    sha256 text NOT NULL CHECK (sha256 = '' OR sha256 ~ '^sha256:[0-9a-f]{64}$'),
    storage_key text NOT NULL CHECK (length(storage_key) BETWEEN 1 AND 512),
    sealed_key bytea NOT NULL CHECK (octet_length(sealed_key) BETWEEN 1 AND 8192),
    token uuid,
    lease_expires_at timestamptz,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK ((kind='upload' AND execution_id IS NULL AND name='') OR (kind='output' AND execution_id IS NOT NULL AND name<>'')),
    CHECK (status<>'ready' OR sha256<>''),
    CHECK (status NOT IN ('uploading','deleting') OR (token IS NOT NULL AND lease_expires_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS execution_files_expiry_idx ON execution_files (expires_at);
CREATE INDEX IF NOT EXISTS execution_files_account_idx ON execution_files (account_id);
CREATE INDEX IF NOT EXISTS execution_files_execution_idx ON execution_files (execution_id);
CREATE TABLE IF NOT EXISTS execution_file_links (
    execution_id uuid NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    file_id uuid NOT NULL REFERENCES execution_files(id),
    PRIMARY KEY (execution_id,file_id)
);
CREATE INDEX IF NOT EXISTS execution_file_links_file_idx ON execution_file_links(file_id);

-- +goose Down
SELECT 1; -- forward-only expansion; retained files must remain readable
