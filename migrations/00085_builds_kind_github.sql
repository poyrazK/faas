-- +goose Up
-- +goose StatementBegin
-- Issue #432 phase 5 follow-up: cmd/githubd pushes each touched
-- app's enqueue through RealService.CreateDeploymentFromPush, which
-- creates a deployment + build row tagged Kind="github"
-- (pkg/state.DeploymentKindGitHub). The pre-existing builds.kind
-- CHECK (relaxed by 00018 to allow 'railpack','dockerfile','tarball')
-- rejects 'github'. The pre-existing deployments.kind CHECK (added
-- by 00002_app_manifest_and_domains.sql, allows 'image','tarball',
-- 'dockerfile') ALSO rejects 'github'. Loosen both.
--
-- Mirrors the loud-fail posture used by 00018/00010/00013: the Down
-- will fail if any 'github'-kinded builds or deployments have been
-- written since the Up.
alter table deployments drop constraint if exists deployments_kind_check;
alter table deployments add constraint deployments_kind_check
  check (kind in ('image','tarball','dockerfile','github'));

alter table builds drop constraint if exists builds_kind_check;
alter table builds add constraint builds_kind_check
  check (kind in ('railpack','dockerfile','tarball','github'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table deployments drop constraint if exists deployments_kind_check;
alter table deployments add constraint deployments_kind_check
  check (kind in ('image','tarball','dockerfile'));

alter table builds drop constraint if exists builds_kind_check;
alter table builds add constraint builds_kind_check
  check (kind in ('railpack','dockerfile','tarball'));
-- +goose StatementEnd