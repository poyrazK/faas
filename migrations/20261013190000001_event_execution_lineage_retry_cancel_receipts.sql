-- +goose Up
ALTER TABLE event_execution_lineage_retry_control_receipts
 DROP CONSTRAINT event_execution_lineage_retry_control_receipts_action_check,
 ADD CONSTRAINT event_execution_lineage_retry_control_receipts_action_check
 CHECK(action IN ('pause','resume','cancel'));

-- +goose Down
-- The narrower CHECK deliberately refuses downgrade while cancellation receipts
-- remain. Stop UUID-keyed cancellations and expire/prune their parent jobs first;
-- do not discard retained command deduplication evidence.
ALTER TABLE event_execution_lineage_retry_control_receipts
 DROP CONSTRAINT event_execution_lineage_retry_control_receipts_action_check,
 ADD CONSTRAINT event_execution_lineage_retry_control_receipts_action_check
 CHECK(action IN ('pause','resume'));
