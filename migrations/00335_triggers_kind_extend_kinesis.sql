-- filename: 00335_triggers_kind_extend_kinesis.sql
-- +goose Up
-- +goose StatementBegin

-- Closed-vocab widening (issue #757 follow-on, Stage 2 PR-A, kind=kinesis).

ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
  CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue',
                  'msk','kinesis'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
  CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue',
                  'msk'));
-- +goose StatementEnd
