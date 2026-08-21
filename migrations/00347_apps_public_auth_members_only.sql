-- filename: 00347_apps_public_auth_members_only.sql
-- +goose Up
-- +goose StatementBegin

-- ADR-120: per-app ingress 'members_only' mode. Closes the
-- second bullet of the canonical ingress-control matrix
-- (Public / Organization members only / Selected IP ranges /
-- Internal Gregale services only). ADR-118 reserved the enum
-- value as a future extension point and called out the
-- prerequisites in §Out-of-scope (line 239-247); ADR-119
-- re-listed it as future-work (§Future work line 313-326);
-- ADR-120 is that follow-on.
--
-- When apps.public_auth_mode='members_only', every public
-- request must carry a valid IAM-6 session cookie
-- (`faas_sid`, sealed under host AEAD — see ADR-076 +
-- pkg/auth/middleware) whose principal has an active
-- membership in apps.org_id. The gate runs at
-- pkg/gateway/handler.go::applyIngressMembersOnly (between
-- applyIngressInternalSvc and applyEdgeRuleIP), delegating
-- the membership check to pkg/authz.IsOrgMember.
--
-- Schema change is a CHECK widening only — no new column.
-- The membership lives in the org_memberships table (IAM-6 /
-- ADR-061); the app row just pins the policy + carries
-- apps.org_id. Mirroring the pattern from
-- migrations/00333_apps_public_auth_internal_only.sql:48-54
-- (the internal_only widening), we DROP + ADD the
-- constraint with the full vocabulary literal because
-- Postgres 15 (CI) rejects ADD CONSTRAINT IF NOT EXISTS.
--
-- Slot note: 00347. The slot is the next free one above
-- the latest real migration (00346_deployments_annotation
-- shipped via PR #984). No bridge fences are needed
-- (00346 is the immediate predecessor; no concurrent PRs
-- are known to target 00347 at the time of authoring).
-- If a sibling PR claims 00347 in flight, the ADR-041
-- renumber dance applies (drop the real file, claim the
-- sibling's fence) — accept the loss; the embed
-- contiguity is preserved by whichever PR merges first.

alter table apps drop constraint if exists apps_public_auth_mode_chk;
alter table apps add constraint apps_public_auth_mode_chk
  check (public_auth_mode in ('open','bearer','basic','ip_allowlist','internal_only','members_only'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down-migrate ordering is load-bearing. We must DROP any
-- rows whose public_auth_mode='members_only' BEFORE
-- narrowing the CHECK constraint: any such row would fail
-- the new CHECK with SQLSTATE 23514 and abort the entire
-- Down with the constraint swap half-applied. Pattern from
-- migrations/00333_apps_public_auth_internal_only.sql:79-95
-- (the internal_only Down) and migrations/00326_apps_public_auth_ip_allowlist.sql:131-141
-- (the ip_allowlist Down).
--
-- The actual mode UPDATE that drops the rows is the
-- responsibility of an operator pre-Down (the migration
-- itself cannot safely UPDATE because the row may have
-- been the only one in the customer's account, and silently
-- deleting would lose data). The Down section narrows the
-- CHECK and accepts a SQLSTATE 23514 if rows remain — the
-- operator must clear them before the Down can complete.
-- Documented here for the operator; pin via
-- DownGrade_NarrowsAndAcceptsRowsPresent in the companion
-- test.

alter table apps drop constraint if exists apps_public_auth_mode_chk;
alter table apps add constraint apps_public_auth_mode_chk
  check (public_auth_mode in ('open','bearer','basic','ip_allowlist','internal_only'));

-- +goose StatementEnd