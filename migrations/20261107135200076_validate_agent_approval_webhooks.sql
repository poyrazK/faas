-- ADR-20261107140000 §4: validates the account webhook CHECK that
-- 20261107135158316 widened NOT VALID, in a later transaction, so the scan
-- runs without the ADD's lock blocking writes.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

-- +goose Down
-- Validation leaves nothing to undo.
SELECT 1;
