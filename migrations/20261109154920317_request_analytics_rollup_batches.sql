-- filename: 20261109154920317_request_analytics_rollup_batches.sql
-- migration-compat: expand
-- ADR-20261103113952 amendment 5: gateways count their own requests into
-- per-minute analytics rollups and send them as batches; apid records each
-- batch_id here in the transaction that applies the batch, so a batch the
-- gateway resends after a lost acknowledgement is applied once. meterd
-- sweeps entries past a day, far longer than a gateway retries. The table is
-- new: the previous release neither reads nor writes it. Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS request_analytics_rollup_batches (
    batch_id uuid PRIMARY KEY,
    node_id text NOT NULL CHECK (octet_length(node_id) <= 255),
    applied_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE request_analytics_rollup_batches IS 'Gateway analytics rollup batches apid applied; written only by apid, swept only by meterd (ADR-20261103113952 amendment 5).';

CREATE INDEX IF NOT EXISTS request_analytics_rollup_batches_applied_idx
    ON request_analytics_rollup_batches (applied_at);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS request_analytics_rollup_batches;
