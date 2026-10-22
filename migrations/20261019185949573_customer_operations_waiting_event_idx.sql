-- migration-compat: expand
-- ADR-20261018183000: apid finds parents waiting on the children-settled
-- event. Waiting Operations are a small share of the table; the partial index
-- keeps that scan off customer_operations. Built concurrently so the table
-- keeps taking writes; replacing an interrupted build also removes the invalid
-- index PostgreSQL leaves behind.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS customer_operations_waiting_event_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS customer_operations_waiting_event_idx
    ON customer_operations ((record -> 'waiting' ->> 'event'))
    WHERE state = 'waiting';
RESET lock_timeout;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS customer_operations_waiting_event_idx;
