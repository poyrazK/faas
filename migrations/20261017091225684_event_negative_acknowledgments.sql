-- +goose Up
ALTER TABLE event_delivery_acknowledgments ADD COLUMN IF NOT EXISTS rejection_status jsonb;
ALTER TABLE event_delivery_acknowledgments DROP CONSTRAINT IF EXISTS event_delivery_acknowledgments_phase_check;
ALTER TABLE event_delivery_acknowledgments ADD CONSTRAINT event_delivery_acknowledgments_phase_check CHECK (phase IN ('offered','awaiting','completed','rejected'));
ALTER TABLE event_delivery_acknowledgments DROP CONSTRAINT IF EXISTS event_delivery_acknowledgments_check;
ALTER TABLE event_delivery_acknowledgments ADD CONSTRAINT event_delivery_acknowledgments_check CHECK ((phase IN ('completed','rejected')) = (completion_digest IS NOT NULL));
ALTER TABLE event_delivery_acknowledgments DROP CONSTRAINT IF EXISTS event_delivery_acknowledgments_rejection_check;
ALTER TABLE event_delivery_acknowledgments ADD CONSTRAINT event_delivery_acknowledgments_rejection_check CHECK ((phase='rejected')=(rejection_status IS NOT NULL));
ALTER TABLE event_acknowledgment_outcomes DROP CONSTRAINT IF EXISTS event_acknowledgment_outcomes_kind_check;
ALTER TABLE event_acknowledgment_outcomes ADD CONSTRAINT event_acknowledgment_outcomes_kind_check CHECK (kind IN ('accepted','renewed','renewal_rejected','completed','expired','retry_requested','dead_letter_requested'));
-- +goose Down
SELECT 1;
