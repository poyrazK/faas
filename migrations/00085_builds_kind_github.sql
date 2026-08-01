-- +goose Up
-- +goose StatementBegin
-- Issue #432 phase 5 follow-up: cmd/githubd pushes each touched
-- app's enqueue through RealService.CreateDeploymentFromPush, which
-- creates a build row tagged Kind="github" (pkg/state.DeploymentKindGitHub).
-- The pre-existing builds.kind CHECK (relaxed by 00018 to allow
-- 'railpack','dockerfile','tarball') rejects 'github'. Loosen the
-- CHECK to include the new kind.
--
-- The deployments.kind column already accepts any text value (no
-- CHECK on deployments_kind; verified via grep). Mirrors the
-- loud-fail posture used by 00018/00010/00013: the Down will fail if
-- any 'github'-kinded builds have been written since the Up.
alter table builds drop constraint if exists builds_kind_check;
alter table builds add constraint builds_kind_check
  check (kind in ('railpack','dockerfile','tarball','github'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table builds drop constraint if exists builds_kind_check;
alter table builds add constraint builds_kind_check
  check (kind in ('railpack','dockerfile','tarball'));
-- +goose StatementEnd