-- +goose Up
-- ADR-208: a non-transactional fleet-wide token correlates one service route
-- publication with acknowledgements from every serving gateway. Aborted or
-- timed-out handoffs may leave gaps; a later attempt must never reuse a token.
CREATE SEQUENCE IF NOT EXISTS deployment_route_generation_seq AS bigint START WITH 1 INCREMENT BY 1;

-- +goose Down
DROP SEQUENCE IF EXISTS deployment_route_generation_seq;
