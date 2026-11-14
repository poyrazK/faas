-- migration-compat: expand

-- +goose Up
-- Events hunt #9: the publish trigger copies every candidate recipient's
-- filter into the event's recipient_snapshot, so an unbounded filter
-- amplified storage per event. apid and the store cap a filter at
-- api.EventSubscriptionFilterMaxBytes (8192) of compact JSON; this backstop
-- allows api.EventSubscriptionFilterStoredMaxBytes (16384) of jsonb text,
-- which adds a space after each ':' and ','. It is added NOT VALID so rows
-- that predate the cap are not scanned under the ADD's lock and keep
-- delivering; a later migration may VALIDATE it once none exceed it.
-- The existence check makes a replay a no-op.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
        WHERE conrelid = 'event_subscriptions'::regclass
          AND conname = 'event_subscriptions_filter_size_chk') THEN
        ALTER TABLE event_subscriptions
            ADD CONSTRAINT event_subscriptions_filter_size_chk
            CHECK (octet_length(filter::text) <= 16384) NOT VALID;
    END IF;
END;
$do$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE event_subscriptions DROP CONSTRAINT IF EXISTS event_subscriptions_filter_size_chk;
