-- Owner maintenance scans by retention cutoff. Indexes do not change the
-- application contract. Expiry becomes effective only when a receipt is purged.
CREATE INDEX note_create_receipts_retention_idx
  ON api.note_create_receipts (created_at, subject, request_key);
