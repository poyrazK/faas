-- filename: 20261104133714242_edge_tls_certificates.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
-- ADR-20261103163954 §3: the apps domains' publicly trusted certificates,
-- which edgednsd obtains from Let's Encrypt on the control plane. edgednsd is
-- the only writer; a compute edge reads the newest live row for its names.
-- The key is sealed to the edge-tls seal domain, which only the compute
-- edges' gatewayd-public open. A row is kept for a while after it expires,
-- so the Certificate Transparency watch can tell our certificates from
-- anyone else's. The previous release ignores the table.
CREATE TABLE IF NOT EXISTS edge_tls_certificates (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    names       text[]      NOT NULL CHECK (cardinality(names) BETWEEN 1 AND 10),
    ca          text        NOT NULL CHECK (ca IN ('letsencrypt', 'letsencrypt-staging')),
    account_uri text        NOT NULL CHECK (account_uri ~ '^https://' AND octet_length(account_uri) <= 512),
    serial      text        NOT NULL CHECK (serial ~ '^[0-9a-f]{1,64}$'),
    sha256      text        NOT NULL UNIQUE CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    chain_pem   text        NOT NULL CHECK (octet_length(chain_pem) BETWEEN 1 AND 65536),
    key_sealed  bytea       NOT NULL CHECK (octet_length(key_sealed) BETWEEN 1 AND 65536),
    key_kid     text        NOT NULL CHECK (octet_length(key_kid) BETWEEN 1 AND 256),
    not_before  timestamptz NOT NULL CHECK (isfinite(not_before)),
    not_after   timestamptz NOT NULL CHECK (isfinite(not_after) AND not_after > not_before),
    issued_at   timestamptz NOT NULL DEFAULT now() CHECK (isfinite(issued_at))
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS edge_tls_certificates_names_not_after
    ON edge_tls_certificates (names, not_after DESC);
COMMENT ON TABLE edge_tls_certificates IS
    'ADR-20261103163954 the apps domains'' certificates; edgednsd is the only writer, the key is sealed to the edge-tls seal domain.';

-- +goose Down
DROP TABLE IF EXISTS edge_tls_certificates;
