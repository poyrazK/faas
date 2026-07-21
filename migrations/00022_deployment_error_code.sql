-- +goose Up
-- +goose StatementBegin

-- ADR-021 (image digest enforcement hardening, G1): durable carrier
-- for the RFC 7807 failure code that imaged writes when a
-- deployment transitions to `failed`. pkg/api.SentinelToCode maps
-- the three puller-side sentinels (oci.ErrImageNotFound,
-- oci.ErrImageEgressDenied, oci.ErrImageManifestInvalid) to the
-- codes pkg/api.CodeImage* ; imaged writes the code via
-- pkg/state.SetDeploymentFailed at the markDeployFailed helper.
--
-- Persisted separately from `deployments.error` (free text) so
-- audits and the M7.5 dashboard can GROUP BY failure mode without
-- parsing strings — spec §11 requires security-relevant decisions
-- (egress denials surface here as CodeImageEgressDenied / 403) to
-- be observable in pg.
--
-- Idempotent (IF NOT EXISTS) so the migration can be re-run during
-- local development. NULL on every existing row; backfill N/A.

alter table deployments
  add column if not exists error_code text;

-- Index: the M7.5 dashboard's "deployments that failed in the
-- last 24h, grouped by error_code" query reads with WHERE
-- status='failed' AND error_code=$1 ; partial index keeps it
-- tiny on a box whose deployments table is dominated by
-- status='live'. Mirrors the indexes on apps / accounts for the
-- same scan shape.

create index if not exists deployments_failed_error_code_idx
  on deployments (error_code)
  where status = 'failed';

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
drop index if exists deployments_failed_error_code_idx;
alter table deployments drop column if exists error_code;
-- +goose StatementEnd
