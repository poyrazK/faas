-- filename: 20260930215817770_invoice_detail_lifecycle.sql

-- +goose Up
ALTER TABLE invoices ADD COLUMN detail_lifecycle jsonb NOT NULL DEFAULT '{}'::jsonb
  CONSTRAINT invoices_detail_lifecycle_object_check CHECK (jsonb_typeof(detail_lifecycle) = 'object'),
  -- Mirrors api.MaxInvoiceLifecycleRecords; each provider ID has charge/tax history.
  ADD CONSTRAINT invoices_detail_lifecycle_records_check CHECK (
    NOT (detail_lifecycle ? 'records') OR (
      jsonb_typeof(detail_lifecycle->'records') = 'object' AND
      jsonb_array_length(jsonb_path_query_array(detail_lifecycle, '$.records.keyvalue()')) <= 20002
    )
  );

-- +goose Down
ALTER TABLE invoices DROP COLUMN detail_lifecycle;
