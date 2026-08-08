-- filename: 00165_build_provenance_framework_version.sql
-- +goose Up
-- +goose StatementBegin
-- Issue #740 / DEPLOY-PROV-5: surface the language version the
-- customer's source declares (engines.node, requires-python, go.mod
-- directive, .nvmrc, .python-version, .tool-versions) so the operator
-- can debug "why did my app pick Python 3.11?" without reading
-- builder logs. The new column is nullable and never read by the
-- build pipeline — it is post-mortem metadata only, mirroring the
-- build_provenance.*_version siblings (buildkit_version /
-- railpack_version). ADR-087 §3 names this column as the
-- customer-visible version surface; apps.runtime remains
-- operator-controlled and pipeline-binding.
--
-- Slot 165 is the next free slot on origin/main (164 was taken by
-- gdpr_request_id; no fence needed because no sibling PR is racing
-- for 165).
--
-- Replay-safe (ADR-041): ALTER TABLE ADD COLUMN is idempotent in
-- Postgres when guarded by the IF NOT EXISTS clause; the partial
-- index CREATE INDEX IF NOT EXISTS is the standard defence in depth.
ALTER TABLE build_provenance
  ADD COLUMN IF NOT EXISTS framework_version text;

CREATE INDEX IF NOT EXISTS build_provenance_framework_version_idx
  ON build_provenance (framework_version)
  WHERE framework_version IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS build_provenance_framework_version_idx;
ALTER TABLE build_provenance
  DROP COLUMN IF EXISTS framework_version;
-- +goose StatementEnd
