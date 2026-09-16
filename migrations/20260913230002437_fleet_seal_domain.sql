-- +goose Up
-- +goose StatementBegin

-- Dedicated fleet-wide age identity readiness probe (issue #2437).
-- The payload is intentionally non-secret and is sealed through the same
-- secretbox envelope path as customer app secrets. A joining node must open
-- it before its drained compute_nodes row can be activated.
CREATE TABLE IF NOT EXISTS fleet_seal_domain_probe (
    id          smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    recipient   text NOT NULL CHECK (recipient ~ '^age1[0-9a-z]+$'),
    sealed_blob bytea NOT NULL CHECK (octet_length(sealed_blob) > 0),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE fleet_seal_domain_probe IS
    'Singleton non-secret secretbox probe proving every admitted node shares the fleet.age unseal domain.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS fleet_seal_domain_probe;

-- +goose StatementEnd
