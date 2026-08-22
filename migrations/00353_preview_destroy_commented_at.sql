-- filename: 00353_preview_destroy_commented_at.sql
-- +goose Up
-- +goose StatementBegin

-- Mega-C PR-1 / issue #961 leaf 3: one-click preview destroy from
-- a PR comment. pkg/githubd/service.go's handlePullRequest now
-- posts a tear-down hint comment to GitHub when a preview is
-- created, and the same comment is re-posted (deduped) on
-- subsequent closed events. The dedupe key is
-- (app_id, pr_number) — stored on the preview apps row itself
-- so a single column carries the entire dedupe state. Two
-- reasons for putting it on apps rather than a sidecar table:
--
--   1. The row is already gated on account_id + slug PK, so
--      no new index is needed for the dedupe read.
--   2. When the row is destroyed via the new
--      POST /v1/preview/{slug}/destroy endpoint the column
--      disappears with it — no GC sweep.
--
-- Replay-safe: ADD COLUMN IF NOT EXISTS, nullable, no default.
-- The githubd dispatcher writes the column on every comment
-- post; reads are filtered by IS NULL.

ALTER TABLE apps
  ADD COLUMN IF NOT EXISTS preview_destroy_commented_at TIMESTAMPTZ NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE apps DROP COLUMN IF EXISTS preview_destroy_commented_at;

-- +goose StatementEnd