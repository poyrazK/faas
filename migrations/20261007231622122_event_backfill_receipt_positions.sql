-- +goose Up
-- Acceptance ordinality stays immutable. Added consumers receive append-only
-- positions under the parent receipt lock, independent of job retention.
ALTER TABLE event_fanout_recipients
    ADD COLUMN receipt_position bigint CHECK (receipt_position > 0);

WITH additions AS (
    SELECT r.outbox_id, r.subscription_id,
           jsonb_array_length(coalesce(o.recipient_snapshot, '[]'::jsonb)) +
           row_number() OVER (PARTITION BY r.outbox_id ORDER BY j.created_at NULLS LAST, j.id, r.subscription_id) AS position
    FROM event_fanout_recipients r
    JOIN event_fanout_outbox o ON o.id=r.outbox_id
    LEFT JOIN event_replay_jobs j ON j.id=r.backfill_job_id
    WHERE NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb)) s(recipient)
        WHERE s.recipient->>'id'=r.subscription_id
    )
)
UPDATE event_fanout_recipients r SET receipt_position=a.position
FROM additions a WHERE r.outbox_id=a.outbox_id AND r.subscription_id=a.subscription_id;

CREATE UNIQUE INDEX event_fanout_recipients_receipt_position_idx
    ON event_fanout_recipients(outbox_id, receipt_position)
    WHERE receipt_position IS NOT NULL;

-- +goose Down
DROP INDEX event_fanout_recipients_receipt_position_idx;
ALTER TABLE event_fanout_recipients DROP COLUMN receipt_position;
