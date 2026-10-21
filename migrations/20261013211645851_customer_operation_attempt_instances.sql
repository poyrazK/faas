-- ADR-1195: the instance that ran each HTTP operation attempt. The
-- invocation row keeps only the latest instance and instance rows are reaped
-- after parking, so the timeline needs its own durable placement record to
-- point an attempt at its runtime logs. Rows follow their execution.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS customer_operation_attempt_instances (
  operation_id uuid NOT NULL,
  generation integer NOT NULL CHECK (generation > 0),
  attempt integer NOT NULL CHECK (attempt > 0),
  instance_id uuid NOT NULL,
  placed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (operation_id, generation, attempt),
  FOREIGN KEY (operation_id, generation)
    REFERENCES customer_operation_executions(operation_id, generation) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE customer_operation_attempt_instances;
-- +goose StatementEnd
