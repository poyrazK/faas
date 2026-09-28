-- +goose Up
-- +goose StatementBegin
-- Keep a bounded-by-event-retention audit trail for recipient routing
-- outcomes. recipient_progress remains the mutable scheduler checkpoint;
-- this table preserves the snapshots that operator replay would otherwise
-- replace.
CREATE TABLE IF NOT EXISTS event_fanout_attempt_history (
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

CREATE INDEX IF NOT EXISTS event_fanout_attempt_history_app_idx
    ON event_fanout_attempt_history (app_id, id DESC);
CREATE INDEX IF NOT EXISTS event_fanout_attempt_history_recipient_idx
    ON event_fanout_attempt_history (outbox_id, subscription_id, id DESC);

-- Preserve the latest checkpoint for already accepted events when this
-- migration is applied. Earlier transitions cannot be reconstructed, but
-- current outcomes remain inspectable after a future replay.
WITH backfill AS (
    SELECT o.id AS outbox_id,
           (r.recipient->>'app_id')::uuid AS app_id,
           p.key AS subscription_id,
           'fanout_attempt'::text AS action,
           p.outcome->>'state' AS state,
           COALESCE(NULLIF(p.outcome->>'attempts', '')::integer, 0) AS attempts,
           COALESCE(p.outcome->>'failure_code', '') AS failure_code,
           COALESCE(NULLIF(p.outcome->>'retryable', '')::boolean, false) AS retryable,
           COALESCE(p.outcome->>'last_error', '') AS last_error,
           COALESCE(NULLIF(p.outcome->>'updated_at', '')::timestamptz, o.created_at) AS occurred_at
    FROM event_fanout_outbox AS o
    CROSS JOIN LATERAL jsonb_each(COALESCE(o.recipient_progress, '{}'::jsonb)) AS p(key, outcome)
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
    WHERE r.recipient->>'id' = p.key
      AND p.outcome->>'state' IN ('pending', 'filtered', 'enqueued', 'failed')
)
INSERT INTO event_fanout_attempt_history
    (outbox_id, app_id, subscription_id, action, state, attempts,
     failure_code, retryable, last_error, occurred_at)
SELECT b.outbox_id, b.app_id, b.subscription_id, b.action, b.state, b.attempts,
       b.failure_code, b.retryable, b.last_error, b.occurred_at
FROM backfill AS b
WHERE NOT EXISTS (
    SELECT 1
    FROM event_fanout_attempt_history AS h
    WHERE h.outbox_id = b.outbox_id
      AND h.app_id = b.app_id
      AND h.subscription_id = b.subscription_id
      AND h.action = b.action
      AND h.state = b.state
      AND h.attempts = b.attempts
      AND h.failure_code = b.failure_code
      AND h.retryable = b.retryable
      AND h.last_error = b.last_error
      AND h.occurred_at = b.occurred_at
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS event_fanout_attempt_history;
-- +goose StatementEnd
