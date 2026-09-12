-- +goose Up
-- +goose StatementBegin
-- Public status history (issue #276): bound the daily invocation rollup and
-- recent incident timeline to their time predicates. These indexes are
-- additive; invocation/result retention remains owned by the async-job
-- retention policy.
CREATE INDEX IF NOT EXISTS invocations_status_history_idx
    ON invocations (created_at)
    WHERE state IN ('completed', 'failed', 'cancelled', 'dead_letter')
       OR outcome IS NOT NULL;

CREATE INDEX IF NOT EXISTS status_incidents_timeline_idx
    ON status_incidents (posted_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS status_incidents_timeline_idx;
DROP INDEX IF EXISTS invocations_status_history_idx;
-- +goose StatementEnd
