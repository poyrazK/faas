-- filename: 20261021222149804_builds_not_before.sql

-- ADR-20261021221906. A developer sync that already reached the running
-- instance as a live patch (ADR-740) queues its convergence build with
-- not_before set to the end of the coalescing window; every claim path skips
-- a queued build until then. A newer sync cancels the queued row, so a run of
-- saves builds once. NULL keeps the historical claim-immediately behaviour.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE builds ADD COLUMN IF NOT EXISTS not_before timestamptz;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE builds DROP COLUMN IF EXISTS not_before;
-- +goose StatementEnd
