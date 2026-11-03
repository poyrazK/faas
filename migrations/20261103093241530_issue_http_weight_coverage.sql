-- filename: 20261103093241530_issue_http_weight_coverage.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Old writers omit this column. False means a single HTTP record may stand
-- for an unknown number of failures; non-HTTP events always represent one.
ALTER TABLE issue_events ADD COLUMN IF NOT EXISTS occurrence_weight_known boolean NOT NULL DEFAULT false;
-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE issue_events DROP COLUMN IF EXISTS occurrence_weight_known;
