-- ADR-1196: an Operation's deadline is fixed at admission. schedd's limit
-- sweeper finds active Operations past it through the partial index.
-- +goose Up
ALTER TABLE customer_operations ADD COLUMN IF NOT EXISTS deadline_at timestamptz;
CREATE INDEX IF NOT EXISTS customer_operations_active_deadline_idx
    ON customer_operations (deadline_at, id)
    WHERE deadline_at IS NOT NULL AND state IN ('accepted', 'running', 'waiting');

-- +goose Down
DROP INDEX IF EXISTS customer_operations_active_deadline_idx;
ALTER TABLE customer_operations DROP COLUMN IF EXISTS deadline_at;
