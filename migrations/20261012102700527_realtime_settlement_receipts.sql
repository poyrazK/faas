-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_group_settlement_receipts (
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 principal text NOT NULL CHECK (principal='account' OR principal~'^[0-9a-f-]{36}$'),
 idempotency_key uuid NOT NULL,
 request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
 result jsonb NOT NULL CHECK (jsonb_typeof(result)='object' AND octet_length(result::text)<=16384),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at>created_at),
 PRIMARY KEY(group_id,principal,idempotency_key)
);
CREATE INDEX IF NOT EXISTS realtime_settlement_receipts_expiry ON managed_realtime_group_settlement_receipts(group_id,expires_at);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_realtime_group_settlement_receipts WHERE expires_at>clock_timestamp()) THEN
  RAISE EXCEPTION 'live settlement receipts must expire before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE managed_realtime_group_settlement_receipts;
