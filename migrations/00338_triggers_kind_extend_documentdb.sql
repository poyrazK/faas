-- filename: 00338_triggers_kind_extend_documentdb.sql
-- +goose Up
-- +goose StatementBegin

-- Closed-vocab widening (issue #757 follow-on, Stage 2 PR-A,
-- kind=documentdb). Final value: 11 kinds.

ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
  CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue',
                  'msk','kinesis','dynamodb_streams','rabbitmq','documentdb'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_kind_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_kind_check
  CHECK (kind IN ('cron','kafka','nats','redis_streams','sqs_compat','queue',
                  'msk','kinesis','dynamodb_streams','rabbitmq'));
-- +goose StatementEnd
