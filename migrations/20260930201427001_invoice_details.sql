-- +goose Up
ALTER TABLE invoices ADD COLUMN details jsonb NOT NULL DEFAULT '{}'::jsonb
  CONSTRAINT invoices_details_object_check CHECK (jsonb_typeof(details) = 'object'),
  -- Mirrors api.MaxInvoiceSeenLineIDs; retain creation instants after removal.
  ADD CONSTRAINT invoices_details_seen_line_limit_check CHECK (
    jsonb_array_length(jsonb_path_query_array(details, '$.line_first_seen.keyvalue()')) <= 10000
  );

-- +goose Down
ALTER TABLE invoices DROP COLUMN details;
