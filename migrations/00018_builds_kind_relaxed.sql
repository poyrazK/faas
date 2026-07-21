-- +goose Up
-- +goose StatementBegin
-- M6 gap closure: cmd/apid/deploy_inputs.go writes state.DeploymentKindTarball
-- ("tarball") and state.DeploymentKindDockerfile ("dockerfile") when a
-- customer uploads a source tarball or a Dockerfile. Migration 00001's
-- CHECK on builds.kind only allowed ('railpack','dockerfile') — which
-- doesn't actually cover source-deploy tarballs at all, only the framework
-- dispatch names. A paas tarball deploy therefore failed the INSERT in
-- PgStore silently (MemStore does not enforce the CHECK).
--
-- Relax the CHECK to the union of values pkg/state writes. dockerfile is
-- already present; we add 'tarball'. (image — registry deploys — never
-- goes through the builds table, it goes through imaged directly.) The
-- builds.kind column mirrors deployments.kind, which already accepts all
-- three values (migrations/00002).
alter table builds drop constraint if exists builds_kind_check;
alter table builds add constraint builds_kind_check
  check (kind in ('railpack','dockerfile','tarball'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Reverse the relaxation. Note that this will fail the Down if any
-- 'tarball'-kinded builds have been written since the Up — operator
-- must delete or reclassify them first. Matches the loud-fail
-- posture used elsewhere in 00010/00013.
alter table builds drop constraint if exists builds_kind_check;
alter table builds add constraint builds_kind_check
  check (kind in ('railpack','dockerfile'));
-- +goose StatementEnd
