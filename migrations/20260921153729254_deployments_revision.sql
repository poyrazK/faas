-- filename: 20260921153729254_deployments_revision.sql
-- +goose Up
-- +goose StatementBegin

-- Human-addressable deployment revisions (ADR-198).
--
-- Deployments have always been immutable rows, but the only handle a
-- customer could use was the uuid. Every rollout surface that names a
-- revision (gregale rollback --to, gregale traffic set --deployment, the
-- canary ladder, deployment audit) therefore required copying a uuid out
-- of a list. `revision` is the per-app monotonic counter that makes those
-- surfaces addressable as v41 / v42 / v43.
--
-- This column MATERIALIZES the ordinal that Store.DeploymentOrdinal has
-- computed on read since ADR-122 (SAFE-RELEASES-C.2), which stamps the
-- deploy-{N}-{slug}.gregale.dev preview hostname. The partition and the
-- ORDER BY below are deliberately identical to that query
-- (partition by app_id order by created_at, id), so every already-issued
-- preview URL keeps resolving to the same row after this migration.
-- Do NOT partition this by scope: it would fork into a second, different
-- N and silently rot live preview URLs.
--
-- Storing it also makes the ordinal strictly more stable than computing
-- it — a computed row_number() shifts if any earlier row is ever hard
-- deleted, while an assigned revision never moves.
--
-- revision = 0 is the "unassigned" sentinel. Both stores assign a positive
-- revision inside CreateDeployment's existing FOR UPDATE window on the
-- parent apps row, so 0 is unreachable through the supported write path;
-- it exists so a raw-SQL fixture insert that predates this column fails
-- visibly (absent revision in the API projection) instead of tripping a
-- NOT NULL and taking down an unrelated test. The partial unique index
-- below excludes it for the same reason.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS revision integer NOT NULL DEFAULT 0;

-- Backfill: number existing rows per app in creation order. Mirrors
-- DeploymentOrdinal's (created_at, id) ordering exactly so stored and
-- previously-computed ordinals agree for every pre-existing row.
WITH numbered AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY app_id
               ORDER BY created_at, id
           ) AS rn
      FROM deployments
)
UPDATE deployments d
   SET revision = n.rn
  FROM numbered n
 WHERE d.id = n.id
   AND d.revision = 0;

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_revision_nonneg_chk;
ALTER TABLE deployments
    ADD CONSTRAINT deployments_revision_nonneg_chk CHECK (revision >= 0);

-- Partial unique: two rows of one app can never claim the same revision.
-- Excludes the 0 sentinel so unassigned rows are not a uniqueness
-- collision with each other.
CREATE UNIQUE INDEX IF NOT EXISTS deployments_app_revision_uniq
    ON deployments (app_id, revision)
 WHERE revision > 0;

-- Covers both the max(revision) lookup CreateDeployment runs per insert
-- and the DeploymentByRevision point lookup the CLI resolver uses.
CREATE INDEX IF NOT EXISTS deployments_app_revision_desc_idx
    ON deployments (app_id, revision DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS deployments_app_revision_desc_idx;
DROP INDEX IF EXISTS deployments_app_revision_uniq;
ALTER TABLE IF EXISTS deployments
    DROP CONSTRAINT IF EXISTS deployments_revision_nonneg_chk;
ALTER TABLE IF EXISTS deployments
    DROP COLUMN IF EXISTS revision;

-- +goose StatementEnd
