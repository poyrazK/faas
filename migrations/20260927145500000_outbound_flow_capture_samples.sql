-- +goose Up
-- +goose StatementBegin
-- Operator-only capture health evidence. A new session ID is minted at each
-- vmmd start; missing samples themselves signal a coverage gap after crashes
-- and database outages. Keep node identity even if a node row is removed.
CREATE TABLE outbound_flow_capture_samples (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL,
    node_id uuid NOT NULL,
    public_ip inet,
    sampled_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    listening boolean NOT NULL,
    reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 64),
    queue_dropped_total bigint NOT NULL CHECK (queue_dropped_total >= 0),
    database_dropped_total bigint NOT NULL CHECK (database_dropped_total >= 0),
    unparsed_total bigint NOT NULL CHECK (unparsed_total >= 0),
    stderr_total bigint NOT NULL CHECK (stderr_total >= 0)
);
CREATE INDEX outbound_flow_capture_samples_node_time_idx
    ON outbound_flow_capture_samples (node_id, sampled_at, id);
CREATE INDEX outbound_flow_capture_samples_public_time_idx
    ON outbound_flow_capture_samples (public_ip, sampled_at, id);
CREATE INDEX outbound_flow_capture_samples_retention_idx
    ON outbound_flow_capture_samples (sampled_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE outbound_flow_capture_samples;
-- +goose StatementEnd
