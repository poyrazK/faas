-- +goose Up
-- +goose StatementBegin
-- Keep a bounded-by-event-retention audit trail for recipient routing
-- outcomes. recipient_progress remains the mutable scheduler checkpoint;
-- this table preserves the snapshots that operator replay would otherwise
-- replace.
CREATE TABLE event_fanout_attempt_history (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    outbox_id bigint NOT NULL REFERENCES event_fanout_outbox(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    subscription_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('fanout_attempt', 'operator_replay')),
    state text NOT NULL CHECK (state IN ('pending', 'filtered', 'enqueued', 'failed')),
    attempts integer NOT NULL CHECK (attempts >= 0),
    failure_code text NOT NULL DEFAULT '',
    retryable boolean NOT NULL DEFAULT false,
    last_error text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX event_fanout_attempt_history_app_idx
    ON event_fanout_attempt_history (app_id, id DESC);
CREATE INDEX event_fanout_attempt_history_recipient_idx
    ON event_fanout_attempt_history (outbox_id, subscription_id, id DESC);

-- Preserve the latest checkpoint for already accepted events when this
-- migration is applied. Earlier transitions cannot be reconstructed, but
-- current outcomes remain inspectable after a future replay.
INSERT INTO event_fanout_attempt_history
    (outbox_id, app_id, subscription_id, action, state, attempts,
     failure_code, retryable, last_error, occurred_at)
SELECT o.id,
       (r.recipient->>'app_id')::uuid,
       p.key,
       'fanout_attempt',
       p.outcome->>'state',
       COALESCE(NULLIF(p.outcome->>'attempts', '')::integer, 0),
       COALESCE(p.outcome->>'failure_code', ''),
       COALESCE(NULLIF(p.outcome->>'retryable', '')::boolean, false),
       COALESCE(p.outcome->>'last_error', ''),
       COALESCE(NULLIF(p.outcome->>'updated_at', '')::timestamptz, o.created_at)
FROM event_fanout_outbox AS o
CROSS JOIN LATERAL jsonb_each(COALESCE(o.recipient_progress, '{}'::jsonb)) AS p(key, outcome)
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
WHERE r.recipient->>'id' = p.key
  AND p.outcome->>'state' IN ('pending', 'filtered', 'enqueued', 'failed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS event_fanout_attempt_history;
-- +goose StatementEnd
