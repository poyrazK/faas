-- +goose Up
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS details jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conname = 'invoices_details_object_check'
      AND conrelid = 'invoices'::regclass
  ) THEN
    ALTER TABLE invoices
      ADD CONSTRAINT invoices_details_object_check CHECK (jsonb_typeof(details) = 'object');
  END IF;

  -- Mirrors api.MaxInvoiceSeenLineIDs; retain creation instants after removal.
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conname = 'invoices_details_seen_line_limit_check'
      AND conrelid = 'invoices'::regclass
  ) THEN
    ALTER TABLE invoices
      ADD CONSTRAINT invoices_details_seen_line_limit_check CHECK (
        jsonb_array_length(jsonb_path_query_array(details, '$.line_first_seen.keyvalue()')) <= 10000
      );
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE invoices DROP COLUMN details;
