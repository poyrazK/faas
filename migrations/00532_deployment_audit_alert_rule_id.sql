-- filename: 00532_deployment_audit_alert_rule_id.sql
-- +goose Up
-- +goose StatementBegin

-- SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122): add the
-- alert_rule_id FK on deployment_audit so an operator can click
-- through from a deploy.rolled_back audit row to the rule that
-- triggered the rollback. Closes the "which rule fired this?"
-- cross-correlation gap from the 5-PR plan's PR-D stream.
--
-- Two parts:
--   1. Add alert_rule_id UUID NULL column. Nullable because the
--      vast majority of audit rows are NOT rule-triggered — only
--      deploy.alert_rule_fired + the rollback/demote/promote
--      paths stamped via ActionDispatcher carry a non-nil value.
--   2. Partial index (alert_rule_id, at DESC) WHERE alert_rule_id
--      IS NOT NULL. Same predicate-cost argument as the
--      rollout-state indexes PR-A added: only non-null rows
--      participate, so the index stays tiny (one entry per fired
--      rule across the fleet's audit lifetime) and the
--      /dashboard/alerts/{id} reverse-lookup query stays cheap.
--
-- Replay-safety: ALTER TABLE ADD COLUMN IF NOT EXISTS keeps a
-- re-run a no-op; CREATE INDEX IF NOT EXISTS does the same for the
-- index. PG11+ metadata-only for both (nullable column without
-- default = no row rewrite).
--
-- No FK constraint on alert_rule_id: a rule can be deleted while
-- its audit trail outlives it (90-day retention vs operator
-- retention); the FK would block deletion. The application layer
-- is the canonical join; /dashboard/alerts/{id} handler surfaces
-- a "rule no longer exists" chip for the dangling case.

ALTER TABLE deployment_audit
    ADD COLUMN IF NOT EXISTS alert_rule_id UUID;

CREATE INDEX IF NOT EXISTS deployment_audit_alert_rule_idx
    ON deployment_audit (alert_rule_id, at DESC)
    WHERE alert_rule_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only: reversing this column drops the partial index
-- (cheap) then the column (also cheap since no default). No
-- data-loss risk because alert_rule_id was nullable to begin with
-- and zero rows reference it pre-migration.

DROP INDEX IF EXISTS deployment_audit_alert_rule_idx;

ALTER TABLE deployment_audit
    DROP COLUMN IF EXISTS alert_rule_id;

-- +goose StatementEnd