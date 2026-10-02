-- +goose Up
CREATE TABLE IF NOT EXISTS route_health_gates (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 mode text NOT NULL CHECK (mode IN ('report', 'enforce')),
 revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
 routes jsonb NOT NULL CHECK (jsonb_typeof(routes) = 'array' AND jsonb_array_length(routes) <= 20 AND octet_length(routes::text) <= 16384),
 updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE route_health_gates;
