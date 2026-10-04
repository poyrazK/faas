-- +goose Up
CREATE TABLE IF NOT EXISTS saved_route_requirements (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    requirements jsonb NOT NULL CHECK (jsonb_typeof(requirements) = 'object' AND COALESCE(requirements->>'version', '') = '2' AND octet_length(requirements::text) <= 2097152),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE saved_route_requirements;
