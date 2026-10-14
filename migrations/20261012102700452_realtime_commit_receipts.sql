-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_commit_receipts (
    source_id uuid NOT NULL REFERENCES commit_sources(id) ON DELETE CASCADE,
    event_id uuid NOT NULL,
    content_hash bytea NOT NULL CHECK (octet_length(content_hash)=32),
    endpoint_id text NOT NULL,
    channel text NOT NULL,
    sequence bigint NOT NULL CHECK (sequence > 0),
    published_at timestamptz NOT NULL,
    PRIMARY KEY (source_id,event_id)
);

-- +goose Down
DROP TABLE managed_realtime_commit_receipts;
