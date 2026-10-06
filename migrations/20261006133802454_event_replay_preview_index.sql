-- +goose Up
-- Read-only historical preview scans a bounded acceptance-time page per account.
CREATE INDEX event_fanout_outbox_account_accepted_idx ON event_fanout_outbox (account_id, created_at, id);

-- +goose Down
DROP INDEX IF EXISTS event_fanout_outbox_account_accepted_idx;
