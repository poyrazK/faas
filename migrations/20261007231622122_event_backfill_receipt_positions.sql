-- +goose Up
-- Acceptance ordinality stays immutable. Added consumers receive append-only
-- positions under the parent receipt lock, independent of job retention.
ALTER TABLE event_fanout_recipients
    ADD COLUMN IF NOT EXISTS receipt_position bigint CHECK (receipt_position > 0);

WITH positions AS (
    SELECT o.id, greatest(
        jsonb_array_length(coalesce(o.recipient_snapshot, '[]'::jsonb)),
        coalesce(max(r.receipt_position), 0)
    ) AS last_position
    FROM event_fanout_outbox o
    LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id
    GROUP BY o.id
), additions AS (
    SELECT r.outbox_id, r.subscription_id,
           p.last_position +
           row_number() OVER (PARTITION BY r.outbox_id ORDER BY j.created_at NULLS LAST, j.id, r.subscription_id) AS position
    FROM event_fanout_recipients r
    JOIN event_fanout_outbox o ON o.id=r.outbox_id
    JOIN positions p ON p.id=r.outbox_id
    LEFT JOIN event_replay_jobs j ON j.id=r.backfill_job_id
    WHERE r.receipt_position IS NULL AND NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb)) s(recipient)
        WHERE s.recipient->>'id'=r.subscription_id
    )
)
UPDATE event_fanout_recipients r SET receipt_position=a.position
FROM additions a WHERE r.outbox_id=a.outbox_id AND r.subscription_id=a.subscription_id;

CREATE UNIQUE INDEX IF NOT EXISTS event_fanout_recipients_receipt_position_idx
    ON event_fanout_recipients(outbox_id, receipt_position)
    WHERE receipt_position IS NOT NULL;

-- +goose Down
DROP INDEX event_fanout_recipients_receipt_position_idx;
ALTER TABLE event_fanout_recipients DROP COLUMN receipt_position;
