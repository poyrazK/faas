-- +goose Up
-- Independent admission evidence survives Operation result/event retention.
-- NULL marks older receipts whose historical allowance decision was not saved.
-- +goose StatementBegin
DO $product_control_replay$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_control_receipts' AND column_name='admission_evidence') THEN
        ALTER TABLE product_control_receipts ADD COLUMN admission_evidence jsonb
            CHECK (admission_evidence IS NULL OR jsonb_typeof(admission_evidence)='object');
    END IF;
END;
$product_control_replay$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE product_control_receipts DROP COLUMN admission_evidence;
