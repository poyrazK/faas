-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261113120824 §2: validate the workflow-call check added NOT VALID by
-- 20261114154710100, without blocking writes.
ALTER TABLE invocation_sends VALIDATE CONSTRAINT invocation_sends_workflow_call_check;

-- +goose Down
SELECT 1;
