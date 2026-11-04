-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Keep transport reservations after network/account deletion. Reusing a VNI
-- before every old compute node is fenced can join unrelated L2 segments.
CREATE TABLE IF NOT EXISTS private_network_transport_identities (
    network_id text PRIMARY KEY CHECK (length(network_id) BETWEEN 1 AND 128),
    account_id uuid NOT NULL,
    region text NOT NULL CHECK (length(region) BETWEEN 1 AND 64),
    vni integer NOT NULL CHECK (vni BETWEEN 1 AND 16777214),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (region, vni)
);

-- A service address has one physical owner. The address reference prevents
-- release/reallocation until vmmd confirms removal of that owner's network.
CREATE TABLE IF NOT EXISTS private_network_service_owners (
    network_id text NOT NULL REFERENCES private_networks(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE RESTRICT,
    instance_id text NOT NULL CHECK (length(instance_id) BETWEEN 1 AND 128),
    service_address_id uuid NOT NULL UNIQUE REFERENCES private_network_addresses(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (network_id, app_id)
);
CREATE INDEX IF NOT EXISTS private_network_service_instance_idx ON private_network_service_owners (account_id, instance_id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE private_network_service_owners;
DROP TABLE private_network_transport_identities;
