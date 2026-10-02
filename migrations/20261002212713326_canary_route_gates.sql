-- +goose Up
CREATE TABLE IF NOT EXISTS canary_route_gates (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('report', 'enforce')),
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE canary_route_gates;
