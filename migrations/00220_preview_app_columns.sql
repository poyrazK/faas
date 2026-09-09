-- +goose Up
-- +goose StatementBegin

-- 00220_preview_app_columns.sql — ADR-094 (issue #272 PR-preview
-- environments).
--
-- Wires the four columns that mark an `apps` row as a PR preview.
-- The preview path is the third leg of the GitHub deploy pipeline
-- (after issue #739 push-to-deploy and issue #270 explicit-CI
-- faas-deploy-action). Every PR opened against a bound repo
-- provisions a separate apps row at slug `pr-{N}-{parent_slug}`,
-- deploys the PR head SHA into it, routes
-- `pr-{N}-{slug}.gregale.dev` to that row, and tears it down
-- when the PR closes (24h grace, then janitor reaps).
--
-- Columns:
--   preview_of_slug    TEXT  -- parent app slug (informational;
--                                nullable; no FK because the parent
--                                can be deleted while previews are
--                                still open — the dashboard shows
--                                orphans gracefully).
--   preview_pr_number  INT   -- GitHub PR number; zero on prod
--                                apps. Stable across synchronize /
--                                reopened events on the same PR.
--   preview_pr_state   TEXT  -- closed-set: open|closed|stale|
--                                torn_down; NULL on prod apps. The
--                                closed → stale → torn_down
--                                transitions are driven by
--                                cmd/schedd/janitor_preview.go
--                                (PR-C).
--   preview_expires_at TIMESTAMPTZ -- when the teardown janitor
--                                reaps regardless of GitHub state;
--                                created_at + 7 days at provision
--                                time. NULL on prod apps.
--
-- Replay-safety: every ALTER uses IF NOT EXISTS / IF EXISTS guards
-- so a second MigrateUp (e.g. after a partial-apply where the goose
-- row was lost) is idempotent. Same convention as 00213
-- (deployments_scope) and 00203 (app_envs_scope_shape).
--
-- Slot reservation: this migration replaces
-- migrations/00220_reserve_slot.sql, which held the slot before
-- PR-A landed. The fence file is removed in the same PR that
-- introduces this real migration.

ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS preview_of_slug    TEXT        NULL,
    ADD COLUMN IF NOT EXISTS preview_pr_number  INTEGER     NULL,
    ADD COLUMN IF NOT EXISTS preview_pr_state   TEXT        NULL,
    ADD COLUMN IF NOT EXISTS preview_expires_at TIMESTAMPTZ NULL;

-- CHECK constraint on the closed-set preview_pr_state vocabulary.
-- Guarded on pg_catalog.pg_constraint so a replay after a partial-
-- apply doesn't trip "constraint already exists". The Go-side
-- mirror is pkg/state.PreviewPrStateIsValid in types.go.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'apps_preview_pr_state_chk'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_preview_pr_state_chk
            CHECK (preview_pr_state IN ('open','closed','stale','torn_down')
                   OR preview_pr_state IS NULL);
    END IF;
END$$;

-- Partial index on (preview_of_slug) for the dashboard's preview
-- pane lookup ("list all preview rows under this parent app")
-- and the teardown janitor's scan ("find every preview row in
-- the system"). The WHERE preview_of_slug IS NOT NULL predicate
-- keeps the index narrow — production rows never appear.
CREATE INDEX IF NOT EXISTS apps_preview_of_slug_idx
    ON apps (preview_of_slug)
    WHERE preview_of_slug IS NOT NULL;

-- Partial index on (preview_expires_at) for the teardown janitor's
-- "WHERE preview_expires_at < NOW() AND preview_pr_state IN
-- ('closed','stale')" sweep. The WHERE preview_expires_at IS NOT
-- NULL keeps production rows out of the index.
CREATE INDEX IF NOT EXISTS apps_preview_expires_at_idx
    ON apps (preview_expires_at)
    WHERE preview_expires_at IS NOT NULL;

-- Mirror the kind vocabulary loosening from 00085
-- (builds_kind_github) — DeploymentKindPreview is a new value
-- stamped by the preview path. Without this, the apid bridge's
-- INSERT trips 23514. Down-side restores the closed vocabulary;
-- if any preview-kinded rows exist at Down time, the re-add of
-- the CHECK constraint will fail loud (loud-fail posture from
-- 00018 / 00085).
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_kind_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_kind_check
    CHECK (kind IN ('image','tarball','dockerfile','github','preview'));

ALTER TABLE builds DROP CONSTRAINT IF EXISTS builds_kind_check;
ALTER TABLE builds ADD CONSTRAINT builds_kind_check
    CHECK (kind IN ('railpack','dockerfile','tarball','github','preview'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Mirror the Up-shape in reverse. Loud-fail if any preview-kinded
-- rows exist at Down time — the closed vocabulary restored by the
-- rebuilds of the CHECK constraints will reject them with 23514.
ALTER TABLE builds DROP CONSTRAINT IF EXISTS builds_kind_check;
ALTER TABLE builds ADD CONSTRAINT builds_kind_check
    CHECK (kind IN ('railpack','dockerfile','tarball','github'));

ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_kind_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_kind_check
    CHECK (kind IN ('image','tarball','dockerfile','github'));

DROP INDEX IF EXISTS apps_preview_expires_at_idx;
DROP INDEX IF EXISTS apps_preview_of_slug_idx;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'apps_preview_pr_state_chk'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps DROP CONSTRAINT apps_preview_pr_state_chk;
    END IF;
END$$;

ALTER TABLE apps
    DROP COLUMN IF EXISTS preview_expires_at,
    DROP COLUMN IF EXISTS preview_pr_state,
    DROP COLUMN IF EXISTS preview_pr_number,
    DROP COLUMN IF EXISTS preview_of_slug;

-- +goose StatementEnd
