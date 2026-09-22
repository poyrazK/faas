-- +goose Up
-- +goose StatementBegin
-- A split compute rollout finishes all non-disruptive work before it enters
-- the serialized drain/activation lane. Persist that handoff so an activation
-- cannot start from a partial or failed preparation attempt.
ALTER TABLE node_join_jobs
    DROP CONSTRAINT node_join_jobs_phase_check;
ALTER TABLE node_join_jobs
    ADD CONSTRAINT node_join_jobs_phase_check
    CHECK (phase IN ('planned', 'preflight', 'converging', 'prepared', 'verifying', 'active', 'failed', 'rolled_back'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE node_join_jobs
SET phase = 'planned', updated_at = now()
WHERE phase = 'prepared';
ALTER TABLE node_join_jobs
    DROP CONSTRAINT node_join_jobs_phase_check;
ALTER TABLE node_join_jobs
    ADD CONSTRAINT node_join_jobs_phase_check
    CHECK (phase IN ('planned', 'preflight', 'converging', 'verifying', 'active', 'failed', 'rolled_back'));
-- +goose StatementEnd
