-- migration-compat: expand
-- filename: 20261023112631406_invocation_instance_stamp.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- The drain stamps the instance it woke onto a dispatching invocation, in a
-- transaction of its own: the meter's invocation-table fallback counts it, and
-- scenario fault fences check it. Losing the last stamps in a PostgreSQL crash
-- loses only that fallback count (dispatching rows are requeued and stamped
-- again), so the stamp commits without waiting for its WAL flush, which was one
-- flush per invocation (V12 wait sampling, docs/ops/evidence).
CREATE OR REPLACE FUNCTION stamp_invocation_instance(target_invocation uuid, target_instance text)
RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
    PERFORM set_config('synchronous_commit', 'off', true);
    UPDATE invocations SET instance_id = target_instance
    WHERE id = target_invocation AND state = 'dispatching';
    RETURN FOUND;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The meter's fallback count reads the dispatching invocations of one minute
-- by due_at and filters the instance. Without instance_id in an index, the
-- stamp can update the row in place (HOT) instead of writing an entry into
-- every index the row is in.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_dispatching_due_idx
    ON invocations (due_at) WHERE state = 'dispatching';
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_instance_idx;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_instance_idx
    ON invocations (instance_id, due_at) WHERE state = 'dispatching';
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_dispatching_due_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS stamp_invocation_instance(uuid, text);
-- +goose StatementEnd
