-- filename: 20261003140309424_object_bucket_notifications.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE object_bucket_notifications (
    bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision > 0),
    rules jsonb NOT NULL CHECK (jsonb_typeof(rules) = 'array' AND jsonb_array_length(rules) <= 1000)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM object_bucket_notifications WHERE rules <> '[]'::jsonb) THEN
        RAISE EXCEPTION 'cannot discard active bucket notification configuration';
    END IF;
    IF EXISTS (SELECT 1 FROM event_fanout_outbox, jsonb_array_elements(recipient_snapshot) r WHERE r ? 'object_notification') THEN
        RAISE EXCEPTION 'cannot discard retained object notification delivery snapshots';
    END IF;
END $$;
DROP TABLE object_bucket_notifications;
-- +goose StatementEnd
