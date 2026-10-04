-- +goose Up
-- +goose StatementBegin
ALTER TABLE customer_operation_events DROP CONSTRAINT IF EXISTS customer_operation_events_event_type_check;
ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_event_type_check
 CHECK(event_type IN ('accepted','running','progress','artifact_attached','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired'));
-- +goose StatementEnd
-- +goose Down
-- Forward-only; existing verified artifact receipts remain durable.
SELECT 1;
