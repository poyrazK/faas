-- +goose Up
CREATE TABLE execution_artifact_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source_execution_id uuid NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    artifact_name text NOT NULL CHECK (length(artifact_name) BETWEEN 1 AND 256),
    creator_principal_id uuid,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    redeemed_at timestamptz,
    redeemed_execution_id uuid,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT execution_artifact_grants_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT execution_artifact_grants_redeemed_pair_check CHECK ((redeemed_at IS NULL) = (redeemed_execution_id IS NULL))
);

CREATE INDEX execution_artifact_grants_account_source_idx
    ON execution_artifact_grants (account_id, source_execution_id, created_at DESC);

-- +goose Down
DROP TABLE execution_artifact_grants;
