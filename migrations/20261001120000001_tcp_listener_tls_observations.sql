-- Public-edge certificate evidence, separate from apid-owned listener intent.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE app_tcp_listener_tls_observations (
    listener_id UUID NOT NULL REFERENCES app_tcp_listeners(id) ON DELETE CASCADE,
    edge_id TEXT NOT NULL CHECK (octet_length(edge_id) BETWEEN 1 AND 128 AND edge_id = btrim(edge_id) AND edge_id !~ '[[:cntrl:]]'),
    hostname TEXT NOT NULL CHECK (
        char_length(hostname) BETWEEN 1 AND 253
        AND hostname ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'
        AND hostname !~ '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'
    ),
    intent_updated_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL CHECK (observed_at >= intent_updated_at),
    ready BOOLEAN NOT NULL,
    not_after TIMESTAMPTZ,
    PRIMARY KEY (listener_id, edge_id),
    CHECK ((ready AND not_after IS NOT NULL AND not_after > observed_at) OR (NOT ready AND not_after IS NULL))
);
CREATE INDEX app_tcp_listener_tls_observations_observed_at_idx ON app_tcp_listener_tls_observations(observed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE app_tcp_listener_tls_observations;
-- +goose StatementEnd
