-- filename: 20261115170000220_snapshots_sealed_capture_idx.sql
-- migration-compat: expand
-- ADR-20261110210000 slice 3d-3: secretd opens a sealed snapshot's key by
-- its capture id on every sealed wake. Built concurrently so snapshot
-- publication never waits.
-- +goose NO TRANSACTION
-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS snapshots_sealed_capture_idx;
CREATE INDEX CONCURRENTLY snapshots_sealed_capture_idx
 ON snapshots (sealed_capture_id) WHERE sealed_capture_id IS NOT NULL;

RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS snapshots_sealed_capture_idx;
RESET statement_timeout;
