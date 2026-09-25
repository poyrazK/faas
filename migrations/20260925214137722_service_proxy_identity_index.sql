-- +goose Up
-- Source identity is checked on every managed guest call. HostIP slots can
-- be recycled immediately, so a stale time-based cache is unsafe.
CREATE INDEX IF NOT EXISTS instances_live_host_ip_node_idx
    ON instances (host_ip, node_id)
    WHERE state IN ('running', 'draining');

-- +goose Down
DROP INDEX IF EXISTS instances_live_host_ip_node_idx;
