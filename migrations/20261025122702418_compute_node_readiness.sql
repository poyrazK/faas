-- filename: 20261025122702418_compute_node_readiness.sql
-- migration-compat: expand

-- ADR-20261022184531 step 2: a compute node's readiness is proven state.
-- schedd's heartbeat is the only writer: `ready` once the node answers and
-- its vmmd can adopt its configured base generation, `degraded` when it
-- cannot, and `unavailable` / `draining` mirroring lifecycle. Placement
-- prefers ready nodes and uses degraded ones only when no ready node fits.
-- readiness_reason says why a node is not ready.
--
-- The constant default is metadata-only, so adding the column writes no
-- rows, and every existing row reads `ready` (today's placement). The CHECK
-- is added NOT VALID and validated in its own transaction, so heartbeats keep
-- writing during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS readiness text NOT NULL DEFAULT 'ready';

ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS readiness_reason text;

-- +goose StatementBegin
DO $readiness$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'compute_nodes'::regclass AND conname = 'compute_nodes_readiness_chk') THEN
    ALTER TABLE compute_nodes ADD CONSTRAINT compute_nodes_readiness_chk
      CHECK (readiness IN ('ready', 'degraded', 'unavailable', 'draining')) NOT VALID;
  END IF;
END
$readiness$;
-- +goose StatementEnd

ALTER TABLE compute_nodes VALIDATE CONSTRAINT compute_nodes_readiness_chk;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE compute_nodes DROP CONSTRAINT IF EXISTS compute_nodes_readiness_chk;

ALTER TABLE compute_nodes DROP COLUMN IF EXISTS readiness_reason;

ALTER TABLE compute_nodes DROP COLUMN IF EXISTS readiness;

RESET lock_timeout;
