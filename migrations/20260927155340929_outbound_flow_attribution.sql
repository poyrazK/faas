-- +goose Up
-- +goose StatementBegin
-- Connection metadata only: no payload, hostname, URL, or guest secret. IDs and
-- image digest are copied so app/deployment/instance cleanup cannot erase the
-- historical owner. Account deletion still removes the evidence.
CREATE TABLE outbound_flow_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    observed_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    node_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    org_id uuid,
    app_id uuid,
    deployment_id uuid,
    image_digest text,
    source_ip inet NOT NULL,
    source_port integer NOT NULL CHECK (source_port BETWEEN 0 AND 65535),
    destination_ip inet NOT NULL,
    destination_port integer NOT NULL CHECK (destination_port BETWEEN 0 AND 65535),
    protocol text NOT NULL CHECK (protocol IN ('tcp', 'udp')),
    egress_ip inet,
    egress_ip_source text NOT NULL CHECK (egress_ip_source IN ('app_static', 'node_public', 'unknown'))
);
CREATE INDEX outbound_flow_events_egress_time_idx ON outbound_flow_events (egress_ip, observed_at DESC);
CREATE INDEX outbound_flow_events_account_time_idx ON outbound_flow_events (account_id, observed_at DESC);
CREATE INDEX outbound_flow_events_instance_time_idx ON outbound_flow_events (instance_id, observed_at DESC);
CREATE INDEX outbound_flow_events_retention_idx ON outbound_flow_events (observed_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE outbound_flow_events;
-- +goose StatementEnd
