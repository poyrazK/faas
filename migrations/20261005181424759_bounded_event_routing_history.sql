-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_fanout_attempt_history
 ADD COLUMN IF NOT EXISTS capacity_scope text NOT NULL DEFAULT '' CHECK (capacity_scope IN ('','consumer','app','account')),
 ADD COLUMN IF NOT EXISTS capacity_deferrals bigint NOT NULL DEFAULT 0 CHECK (capacity_deferrals >= 0),
 ADD COLUMN IF NOT EXISTS details_truncated boolean NOT NULL DEFAULT false,
 ADD COLUMN IF NOT EXISTS history_bytes bigint GENERATED ALWAYS AS
 (128::bigint + octet_length(subscription_id) + octet_length(action) + octet_length(state)
  + octet_length(failure_code) + octet_length(last_error) + octet_length(capacity_scope)) STORED NOT NULL CHECK (history_bytes >= 0);

CREATE TABLE IF NOT EXISTS event_fanout_history_summaries (
 outbox_id bigint NOT NULL REFERENCES event_fanout_outbox(id) ON DELETE CASCADE,
 subscription_id text NOT NULL,
 app_id uuid NOT NULL,
 observed_outcomes bigint NOT NULL DEFAULT 0 CHECK (observed_outcomes >= 0),
 capacity_deferrals bigint NOT NULL DEFAULT 0 CHECK (capacity_deferrals >= 0),
 coalesced_outcomes bigint NOT NULL DEFAULT 0 CHECK (coalesced_outcomes >= 0),
 compacted_outcomes bigint NOT NULL DEFAULT 0 CHECK (compacted_outcomes >= 0),
 compacted_through_id bigint NOT NULL DEFAULT 0 CHECK (compacted_through_id >= 0),
 compacted_through_at timestamptz,
 first_capacity_wait_at timestamptz,
 last_capacity_wait_at timestamptz,
 last_capacity_scope text NOT NULL DEFAULT '' CHECK (last_capacity_scope IN ('','consumer','app','account')),
 last_outcome_capacity_scope text NOT NULL DEFAULT '' CHECK (last_outcome_capacity_scope IN ('','consumer','app','account')),
 last_was_coalesced boolean NOT NULL DEFAULT false,
 latest_id bigint NOT NULL DEFAULT 0 CHECK (latest_id >= 0),
 latest_failure_id bigint NOT NULL DEFAULT 0 CHECK (latest_failure_id >= 0),
 latest_replay_id bigint NOT NULL DEFAULT 0 CHECK (latest_replay_id >= 0),
 next_prune_at timestamptz,
 PRIMARY KEY (outbox_id,subscription_id)
);
CREATE INDEX IF NOT EXISTS event_fanout_history_summaries_prune_idx
 ON event_fanout_history_summaries (next_prune_at,outbox_id,subscription_id) WHERE next_prune_at IS NOT NULL;

-- Historical capacity checks cannot be reconstructed. Seed known checkpoints
-- and detail counts; the bounded scheduler sweep compacts legacy rows in batches.
INSERT INTO event_fanout_history_summaries
 (outbox_id,subscription_id,app_id,observed_outcomes,capacity_deferrals,latest_id,latest_failure_id,latest_replay_id,next_prune_at)
SELECT h.outbox_id,h.subscription_id,h.app_id,count(*),
 coalesce((o.recipient_progress->h.subscription_id->>'capacity_deferrals')::bigint,0),max(h.id),
 coalesce(max(h.id) FILTER (WHERE h.action='fanout_attempt' AND (h.failure_code<>'' OR h.state='failed')),0),
 coalesce(max(h.id) FILTER (WHERE h.action='operator_replay'),0),'epoch'::timestamptz
FROM event_fanout_attempt_history h JOIN event_fanout_outbox o ON o.id=h.outbox_id
GROUP BY h.outbox_id,h.subscription_id,h.app_id,o.recipient_progress
ON CONFLICT (outbox_id,subscription_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE event_fanout_history_summaries;
ALTER TABLE event_fanout_attempt_history DROP COLUMN history_bytes, DROP COLUMN details_truncated,
 DROP COLUMN capacity_deferrals, DROP COLUMN capacity_scope;
-- +goose StatementEnd
