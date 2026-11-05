-- filename: 20261029092952047_compute_node_wake_breaker.sql
-- migration-compat: expand

-- ADR-20261022184531 step 3: a per-node wake breaker. When a schedd sees
-- api.NodeWakeBreakerFailures consecutive platform-attributable wake failures
-- on a node within the window, it opens the node's breaker until
-- wake_breaker_open_until. Every schedd places by the durable value, so the
-- node is degraded fleet-wide until then. Once it passes, the breaker is
-- half-open: the next platform failure reopens it, and the next successful
-- wake there closes it (sets it NULL). wake_breaker_reason says what opened it.
--
-- Both columns are nullable with no default, so adding them writes no rows.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS wake_breaker_open_until timestamptz;

ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS wake_breaker_reason text;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE compute_nodes DROP COLUMN IF EXISTS wake_breaker_reason;

ALTER TABLE compute_nodes DROP COLUMN IF EXISTS wake_breaker_open_until;

RESET lock_timeout;
