-- migration-compat: expand
-- +goose Up
-- ADR-1323: realtime usage, recorded to review fair-use limits, never billed. Each realtimed
-- process reports its cumulative usage per endpoint for the current UTC hour
-- every minute; a report replaces the row with the larger of the old and new
-- values, so repeats and out-of-order delivery are harmless. A restart gets a
-- new incarnation and therefore a new row. Fleet totals sum the rows of an
-- hour; the fleet peak is the largest per-minute sum of minute_peak_connections.
-- account_id and app_id are copied from the endpoint at ingest and have no
-- foreign keys, so usage survives endpoint, app and account deletion until
-- retention removes it.
CREATE TABLE IF NOT EXISTS realtime_usage_hours (
    endpoint_id             uuid        NOT NULL,
    hour                    timestamptz NOT NULL CHECK (extract(epoch FROM hour)::bigint % 3600 = 0),
    node_name               text        NOT NULL CHECK (length(node_name) BETWEEN 1 AND 253),
    incarnation             uuid        NOT NULL,
    account_id              uuid        NOT NULL,
    app_id                  uuid        NOT NULL,
    minute_peak_connections integer[]   NOT NULL CHECK (cardinality(minute_peak_connections) = 60 AND 0 <= ALL(minute_peak_connections)),
    connection_seconds      bigint      NOT NULL CHECK (connection_seconds >= 0),
    delivered_messages      bigint      NOT NULL CHECK (delivered_messages >= 0),
    delivered_bytes         bigint      NOT NULL CHECK (delivered_bytes >= 0),
    received_messages       bigint      NOT NULL CHECK (received_messages >= 0),
    received_bytes          bigint      NOT NULL CHECK (received_bytes >= 0),
    reported_at             timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (endpoint_id, hour, node_name, incarnation)
);
CREATE INDEX IF NOT EXISTS realtime_usage_hours_account_hour ON realtime_usage_hours (account_id, hour);
CREATE INDEX IF NOT EXISTS realtime_usage_hours_hour ON realtime_usage_hours (hour);

-- +goose Down
DROP TABLE IF EXISTS realtime_usage_hours;
