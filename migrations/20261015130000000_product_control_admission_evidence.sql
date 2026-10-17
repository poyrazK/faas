-- +goose Up
-- Independent admission evidence survives Operation result/event retention.
-- NULL marks older receipts whose historical allowance decision was not saved.
ALTER TABLE product_control_receipts ADD COLUMN admission_evidence jsonb
    CHECK (admission_evidence IS NULL OR jsonb_typeof(admission_evidence)='object');

-- +goose Down
ALTER TABLE product_control_receipts DROP COLUMN admission_evidence;
