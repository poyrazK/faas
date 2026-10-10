-- filename: 20261010145304006_add_deployment_rollback_prepared_at.sql
--
-- ADR-911 follow-up: a plain rollback (`POST /v1/apps/{slug}/rollback`,
-- including the first-wake 5xx auto-rollback) prepares an older revision and
-- asks imaged to activate it. imaged fences automatic promotion of image,
-- GitHub, and preview deployments against newer revisions, which superseded
-- every such rollback target. rollback_prepared_at marks a target prepared by
-- an explicit rollback so imaged promotes it unfenced; promotion clears it.
-- Nullable and additive: existing rows and readers are unaffected.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS rollback_prepared_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments DROP COLUMN IF EXISTS rollback_prepared_at;
-- +goose StatementEnd
