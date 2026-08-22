-- filename: 00302_deployments_stage_state.sql
-- +goose Up
-- +goose StatementBegin

-- 00302_deployments_stage_state.sql — ADR-117 (deploy-stage-progress).
--
-- Adds a `stage_state jsonb` column on `deployments` so the SSE
-- `event: stage` frame can carry per-stage timestamps server-side.
-- ADR-117 pins a closed 6-stage vocabulary:
--
--   source_download
--   dependency_restore
--   image_build
--   security_scan
--   snapshot_prepare
--   readiness
--
-- The column is owned entirely by `pkg/state.Store.AppendDeploymentStage`
-- — handlers never write it directly. The 2s polling loop in
-- `cmd/apid/handlers_ext.go::streamDeploymentLogs` diffs the column
-- against a per-connection `announced map` and emits one
-- `event: stage {name, started_at, duration_ms, status}` frame per
-- transition (in_progress on entry, completed on exit, failed on the
-- active row when DeployFailed fires).
--
-- Why a jsonb column (not a deployment_stages side table):
--   * Per-deployment, read-once per stream connection, never joined,
--     never indexed, never aggregated.
--   * Collapses six writes-per-deploy into one UPDATE per transition.
--   * Mirrors ADR-098's `deployment_scope` overlay choice on
--     `data_upstreams` (single jsonb column over a side table).
--
-- Replay-safety: ADD COLUMN IF NOT EXISTS + DO-block-guarded CHECK.
-- The DO-block is required because PG rejects `ADD CONSTRAINT IF NOT
-- EXISTS` (SQLSTATE 42710 on second pass) — same idiom as
-- 00286_data_upstreams_deployment_scope.sql:51-62 and
-- 00053_deployments_source_url.sql. The harness at
-- migrations/replay_safety_test.go (TestNewMigrationsAreReplaySafe)
-- pins the second pass as a no-op.

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS stage_state jsonb NOT NULL DEFAULT
    '{"current":"source_download","current_started_at":null,"history":[]}'::jsonb;

-- Constrained-name guard per the harness spec at
-- migrations/replay_safety_test.go:91-95 ("constraints + enum-value
-- additions: guard with a DO block that checks pg_constraint first").
-- The CHECK enforces the closed 6-stage vocabulary on
-- `stage_state->>'current'` so a typo from a future contributor
-- lands as SQLSTATE 23514 check_violation at write time rather than
-- leaking as a wire-frame typo. The exact constraint name is
-- `deployments_stage_state_current_check` — pinned in
-- 00302_deployments_stage_state_test.go so a future renaming of the
-- inline CHECK surfaces here, not as a silent no-op.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'deployments_stage_state_current_check'
          AND conrelid = 'deployments'::regclass
    ) THEN
        ALTER TABLE deployments
            ADD CONSTRAINT deployments_stage_state_current_check
                CHECK (stage_state->>'current' IS NULL OR stage_state->>'current' = '' OR stage_state->>'current' IN (
                    'source_download',
                    'dependency_restore',
                    'image_build',
                    'security_scan',
                    'snapshot_prepare',
                    'readiness'
                ));
    END IF;
END$$;

-- +goose StatementEnd

-- +goose Down
-- Forward-only by design. A downgrade would invalidate every
-- AppendDeploymentStage write made under the new column shape; the
-- stage_state jsonb is the durable record of the customer-facing
-- deploy UX, so downgrading without rolling back apid+imaged+builderd
-- would silently break the SSE stream. Mirrors 00287 + 00286 forward-
-- only stance — preserve the wider shape unconditionally on downgrade.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
