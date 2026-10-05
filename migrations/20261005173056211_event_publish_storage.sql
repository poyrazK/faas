-- +goose Up
-- +goose StatementBegin
CREATE TABLE event_storage_admission (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE
);
ALTER TABLE event_fanout_outbox ADD COLUMN customer_storage_bytes bigint
    GENERATED ALWAYS AS (CASE WHEN left(source, 8) = 'gregale.' THEN 0::bigint ELSE
        octet_length(payload::text)::bigint + octet_length(event_data::text)::bigint +
        octet_length(coalesce(recipient_snapshot, '[]'::jsonb)::text)::bigint END) STORED NOT NULL
    CHECK (customer_storage_bytes >= 0);
CREATE INDEX event_fanout_customer_storage_idx ON event_fanout_outbox (account_id)
    INCLUDE (customer_storage_bytes, state, created_at) WHERE customer_storage_bytes > 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX event_fanout_customer_storage_idx;
ALTER TABLE event_fanout_outbox DROP COLUMN customer_storage_bytes;
DROP TABLE event_storage_admission;
-- +goose StatementEnd
