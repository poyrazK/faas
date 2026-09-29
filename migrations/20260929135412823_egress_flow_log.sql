-- +goose Up
-- +goose StatementBegin

-- ADR-371: every destination address and TCP port a tenant guest opened a
-- new flow to, recorded by vmmd from the per-instance egress_flows nft set.
-- It answers "which tenant connected to <ip> at <time>" when an upstream
-- provider or abuse desk reports traffic from the platform's egress
-- address. A row is written when a (destination, port) pair first appears
-- after at least 10 minutes without a new flow to it. Retention is
-- EgressFlowLogRetentionDays (meterd sweeps older rows).
CREATE TABLE IF NOT EXISTS egress_flow_log (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    observed_at timestamptz NOT NULL,
    node_name   text        NOT NULL CHECK (length(node_name) BETWEEN 1 AND 128),
    account_id  text        NOT NULL DEFAULT '' CHECK (length(account_id) <= 64),
    app_id      text        NOT NULL DEFAULT '' CHECK (length(app_id) <= 64),
    instance_id text        NOT NULL CHECK (length(instance_id) BETWEEN 1 AND 128),
    remote_ip   inet        NOT NULL,
    remote_port integer     NOT NULL CHECK (remote_port BETWEEN 0 AND 65535)
);

-- GiST on the address serves both exact and CIDR (<<=) lookups.
CREATE INDEX IF NOT EXISTS egress_flow_log_remote_ip_idx
    ON egress_flow_log USING gist (remote_ip inet_ops);
CREATE INDEX IF NOT EXISTS egress_flow_log_account_idx
    ON egress_flow_log (account_id, observed_at DESC) WHERE account_id <> '';
CREATE INDEX IF NOT EXISTS egress_flow_log_observed_idx
    ON egress_flow_log (observed_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS egress_flow_log;
-- +goose StatementEnd
