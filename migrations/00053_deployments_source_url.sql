-- +goose Up
-- +goose StatementBegin
--
-- 00053_deployments_source_url.sql — Tier 3 (issue #197 B3.10 schema half).
--
-- Slot renumbered 00051 → 00053 by hotfix/main-00051-collision after
-- PR #322 (cosign SBOM) and PR #340 (cron-limits-peppy) both claimed
-- 00051 in parallel sprints and both landed on main, leaving the
-- migrations-check gate broken for every downstream PR. We picked
-- 00053 to keep PR #340's slot (its crons_app_full_idx index was the
-- more recently-deployed 00051, so live prod DBs already have it
-- recorded at version 51; renumbering it would force a re-record
-- that lies). PR #322's deployment-table migration moves to 00053
-- instead — its `if not exists` + drop-then-add guards make the
-- SQL a no-op on a fresh DB that already has the columns from a
-- pre-merge rebase, and on a live prod DB the SQL adds them for
-- the first time (so the slot-53 row in goose_db_version is the
-- honest "applied here" record).
--
-- Adds two columns to deployments:
--
--   source_url — the canonical upstream URL the build was triggered
--                from. For githubd-triggered deploys this is the repo +
--                branch ("https://github.com/acme/app@main"); for
--                registry pulls it is the OCI ref; for tarball /
--                dockerfile deploys it is "" (SourcePath is the spool
--                path on disk; no upstream URL). Optional today; the
--                populator lands in Phase 2 (B3.10 read half).
--
--   commit_sha — the upstream commit SHA the build was triggered
--                from, when known. githubd already passes commitSHA to
--                the CreateDeployment callback
--                (pkg/githubd/service.go:35); Phase 2 reads it and
--                stamps this column. Length-bounded (7..64 hex chars;
--                sha1 fits at 40, sha256 at 64) and regex-anchored to
--                lowercase hex so a pathological 1 MB string OR a
--                64-character string of 'g' is rejected at the DB
--                layer rather than blowing up the row.
--
-- Both columns are nullable. Pre-existing rows from before this
-- migration are unaffected (the "no upstream URL" case is the common
-- one for image: deploys). ADR-038 (Phase 2) names the producer; the
-- reader is /v1/builds/{id}/provenance.
--
-- Down: drops both columns. No data is lost in steady state because
-- both fields are recomputable from the deployment trigger (re-run
-- the githubd createDeployment callback or re-derive source_url from
-- the app config).

ALTER TABLE deployments
    ADD COLUMN source_url text,
    ADD COLUMN commit_sha text;

ALTER TABLE deployments
    ADD CONSTRAINT deployments_commit_sha_shape_chk
        CHECK (commit_sha IS NULL
               OR (char_length(commit_sha) BETWEEN 7 AND 64
                   AND commit_sha ~ '^[0-9a-f]+$'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_commit_sha_shape_chk;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS commit_sha;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS source_url;
-- +goose StatementEnd
