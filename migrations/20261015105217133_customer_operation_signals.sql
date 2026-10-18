-- ADR-1197: a signal delivered to an Operation is recorded in its history.
-- +goose Up
ALTER TABLE customer_operation_events DROP CONSTRAINT IF EXISTS customer_operation_events_event_type_check;
ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_event_type_check
    CHECK (event_type IN ('accepted','running','progress','workflow_progress','result_prepared','artifact_prepared','artifact_attached','milestone','waiting','resumed','approval_decided','signal_received','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired'));

-- +goose Down
-- A signal_received row cannot satisfy the earlier constraint; fail loudly
-- rather than rewrite history.
ALTER TABLE customer_operation_events DROP CONSTRAINT IF EXISTS customer_operation_events_event_type_check;
ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_event_type_check
    CHECK (event_type IN ('accepted','running','progress','workflow_progress','result_prepared','artifact_prepared','artifact_attached','milestone','waiting','resumed','approval_decided','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired'));
