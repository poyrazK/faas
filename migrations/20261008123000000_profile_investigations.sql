-- +goose Up
CREATE TABLE profile_investigations (
    id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    investigation jsonb NOT NULL CHECK (jsonb_typeof(investigation) = 'object' AND octet_length(investigation::text) <= 131072),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX profile_investigations_app_updated_idx ON profile_investigations (app_id, updated_at DESC, id);

-- +goose Down
DROP TABLE profile_investigations;
