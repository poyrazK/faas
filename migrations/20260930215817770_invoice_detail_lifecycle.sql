-- filename: 20260930215817770_invoice_detail_lifecycle.sql

-- +goose Up
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS detail_lifecycle jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conname = 'invoices_detail_lifecycle_object_check'
      AND conrelid = 'invoices'::regclass
  ) THEN
    ALTER TABLE invoices
      ADD CONSTRAINT invoices_detail_lifecycle_object_check
      CHECK (jsonb_typeof(detail_lifecycle) = 'object');
  END IF;

  -- Mirrors api.MaxInvoiceLifecycleRecords; each provider ID has charge/tax history.
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conname = 'invoices_detail_lifecycle_records_check'
      AND conrelid = 'invoices'::regclass
  ) THEN
    ALTER TABLE invoices
      ADD CONSTRAINT invoices_detail_lifecycle_records_check CHECK (
        NOT (detail_lifecycle ? 'records') OR (
          jsonb_typeof(detail_lifecycle->'records') = 'object' AND
          jsonb_array_length(jsonb_path_query_array(detail_lifecycle, '$.records.keyvalue()')) <= 20002
        )
      );
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE invoices DROP COLUMN detail_lifecycle;
