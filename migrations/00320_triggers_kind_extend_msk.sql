-- filename: 00320_triggers_kind_extend_msk.sql
-- +goose Up
-- +goose StatementBegin

-- Closed-vocab widening (issue #757 follow-on, Stage 2 PR-A, kind=msk).
-- Mirrors migrations/00219_edge_rules_kind_limit.sql pattern:
-- Postgres-assigned default name `triggers_kind_check` for the
-- inline CHECK on `kind`; DROP IF EXISTS + ADD pair because PG15
-- has no ADD CONSTRAINT IF NOT EXISTS.

ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
  CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue',
                  'msk'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
  CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue'));
-- +goose StatementEnd
