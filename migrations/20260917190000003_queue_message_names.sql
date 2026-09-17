-- +goose Up
-- +goose StatementBegin

-- Queue bindings name logical queues. Keep the legacy per-app queue
-- compatible by using an empty name for rows created before named queues
-- existed; new sends may select an explicit binding queue.
ALTER TABLE invocations
    ADD COLUMN IF NOT EXISTS queue_name text NOT NULL DEFAULT '';

ALTER TABLE invocations
    DROP CONSTRAINT IF EXISTS invocations_queue_name_shape;

ALTER TABLE invocations
    ADD CONSTRAINT invocations_queue_name_shape
    CHECK (queue_name = '' OR queue_name ~ '^[a-z][a-z0-9-]{0,62}$');

CREATE INDEX IF NOT EXISTS invocations_app_queue_pending_idx
    ON invocations (app_id, queue_name, source, state, due_at)
    WHERE state IN ('pending', 'dispatching');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS invocations_app_queue_pending_idx;
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocations_queue_name_shape;
ALTER TABLE invocations DROP COLUMN IF EXISTS queue_name;
-- +goose StatementEnd
