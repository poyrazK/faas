-- +goose Up
-- +goose StatementBegin
-- Schemas are immutable by version. The first registration for a
-- source/type makes schemaversion mandatory for subsequent publishes.
CREATE TABLE event_schemas (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source text NOT NULL CHECK (char_length(source) BETWEEN 1 AND 256),
    event_type text NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 256),
    version text NOT NULL CHECK (version ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    schema jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, source, event_type, version)
);
CREATE INDEX event_schemas_source_type_idx ON event_schemas
    (account_id, source, event_type);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS event_schemas;
-- +goose StatementEnd
