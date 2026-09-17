-- +goose Up
-- A durable sequence gives every edge-rule mutation a fleet-wide monotonic
-- generation. It is intentionally non-transactional: an aborted mutation may
-- leave a gap, but a later policy can never reuse or decrease a generation.
CREATE SEQUENCE IF NOT EXISTS edge_rule_generation_seq AS bigint START WITH 1 INCREMENT BY 1;

-- +goose Down
DROP SEQUENCE IF EXISTS edge_rule_generation_seq;
