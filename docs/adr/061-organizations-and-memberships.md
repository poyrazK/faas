# ADR-061 · Organizations, memberships, and unpriced seats (IAM-6)

- **Status:** proposed
- **Date:** 2026-08-01
- **Decision:** Introduce an `orgs` table as the tenant for ownership, plan,
  quota, billing, and audit. Keep `accounts` as a human authentication
  identity that belongs to one or more orgs. Ship path-scoped APIs under
  `/v1/orgs/{org_slug}/...`, automatic personal-org compatibility for every
  account, and one existing plan/quota pool per org. Paid per-seat charging
  and SSO are deferred to follow-up issues.

- **Why:** Issue #190. Today `accounts.id` is the only principal: it owns
  every resource, carries the plan and provider identifiers, scopes gateway
  limits, and feeds scheduler / metering admission. A 5-engineer team has
  no way to share a single billing identity, role-gate access, or rotate a
  colleague's key without nuking the host session key. Sales stalls at
  the first team-sized customer. The change must not break §6.2 invariants
  and must land behind the existing personal-account flows so existing
  customers see no behaviour change during the cut-over.

  Two product decisions lock the shape of this ADR (locked with the
  product owner before drafting):

  1. Core teams first: organizations, memberships, invitations, RBAC,
     org-owned resources, and consolidated existing-plan billing. Paid
     per-seat charging and SSO are out of scope.
  2. APIs are path-scoped under `/v1/orgs/{org_slug}/...`. The session
     cookie never permanently binds an "active org" — clients pick the
     org on every request via the URL.

- **Definitions:**

  - *Account:* human authentication identity. Owns email, password,
    MFA, OAuth links, sessions, and login tokens. May belong to zero or
    more organizations. Always belongs to exactly one personal
    organization.
  - *Organization (org):* the shared tenant. Owns apps, projects,
    domains, builds, deployments, secrets, env, API keys, billing
    customer identifiers, dunning state, plan, and quotas. One personal
    organization per account is immutable: it cannot accept additional
    members, transfer ownership, or be deleted independently of the
    account.
  - *Personal org:* the 1-member org that holds an account's existing
    resources through the cut-over and any new resources until the
    customer creates a shared org.
  - *Shared (non-personal) org:* an org with at least one owner, zero or
    more additional members, and exactly one owner in this milestone.
    Membership is capped per plan (value authored in the financial
    model, encoded in `pkg/api/limits.go`); seats are not separately
    billed in this milestone.

- **Role vocabulary** (mirrors the §2 of issue #190 body; the RBAC
  engine keys all membership checks off this set):

  | Role | Capabilities |
  |---|---|
  | `owner` | org lifecycle, ownership transfer, role changes, invitations, resource writes, plan, billing |
  | `admin` | role changes (except owner demotion on the last remaining owner), invitations, resource writes; cannot transfer ownership, change plan, or delete the org |
  | `developer` | application/deployment/configuration writes and operational reads |
  | `viewer` | read-only applications, deployments, usage, audit |
  | `billing` | billing, invoices, plan changes, usage reads; no application mutation |

  Exactly one `owner` exists per non-personal org. The last owner cannot
  self-remove, be removed, or be demoted **by any role, including `admin`**;
  ownership transfer is the only way to vacate the role. PR 2 must enforce
  this in the SQL transaction (`SELECT … FROM org_memberships WHERE org_id
  = $1 AND role = 'owner' AND removed_at IS NULL FOR UPDATE`); PR 5's
  RBAC layer must mirror the same check at the handler so a future
  caller without DB tx access cannot bypass it.

- **Wire / authorization contract:**

  - All org-scoped endpoints resolve the active org via URL slug.
  - Cross-org access returns `404 not_found` (IDOR convention used by
    `pkg/auth/middleware.LoadApp`; never leaks existence).
  - A known member with an insufficient role gets `403 forbidden`.
  - Plan caps surface as dedicated RFC 7807 codes so dashboards and the
    CLI can branch on them without parsing prose (see §"Stable error
    codes" below).
  - Session cookies remain bound to the human account and live `sid`
    only. Selecting an org is a per-request decision and never mutates
    the session envelope.
  - API keys bind to one organization in addition to their existing
    scopes. Effective permission is the intersection of key scopes and
    the creator's current membership role. Removing the creator's
    membership invalidates the key's use immediately.
  - **Legacy keys cannot auto-promote.** A legacy key created before
    PR 6 carries no `org_id`; addressing any path-scoped route with
    such a key returns `CodeOrgAPIKeyRequiresOrg` (409). The
    customer must mint a successor key via `/v1/orgs/{slug}/keys`;
    the legacy row stays in the DB but is no longer useable. This
    avoids the silent-promotion foot-gun (a key whose `account_id`
    happens to belong to a member accidentally widening its scope
    to that member's org).

- **Migration strategy: expansion / backfill / cut-over / contraction**

  The migration is additive over two releases and respects the §17
  invariants. Concretely:

  1. **Expansion** — add nullable `org_id` columns and the new tables
     (`orgs`, `org_memberships`, `org_invitations`). No code reads them
     yet.
  2. **Backfill** — every account gets one personal org and one owner
     membership; the account's plan, status, dunning fields, and provider
     identifiers are copied to the org; every tenant-root row is stamped
     with `org_id`. Slug generation is deterministic and collision-safe
     (a short hash suffix is appended when the slug shape collides).
  3. **Dual-write** — every account creation / plan change / status
     transition / provider write also updates the personal org inside
     the same transaction. Reads continue to use account fields for now.
     Shadow mismatch counters must read zero for one full metering
     cycle before the cut-over proceeds.
  4. **Cut-over** — readers switch to org fields; `account.{plan,status,…}`
     become a compatibility mirror of the personal org. Old flat routes
     keep returning the personal-org data only.
  5. **Contraction** — only after two clean releases: set `org_id` to
     `NOT NULL` on the tenant-root tables and add composite integrity
     constraints. Old account columns may stay one more release for
     rollback, then are removed in an append-only migration.

  `apid` remains the sole writer to customer-intent tables. `schedd`
  remains the sole writer to `instances`. `meterd` continues to walk a
  per-tenant list — that list becomes `orgs` once the cut-over begins.

- **Account deletion and GDPR (ADR-021 compatibility):**

  - Deleting a non-owner member removes their memberships, sessions,
    OAuth / password data, and API keys they created. Shared-org
    resources survive.
  - Deleting the sole owner of a shared org is refused with a
    dedicated 409; ownership transfer must happen first.
  - Deleting an account whose only org is the personal org uses the
    existing 30-day export → restore → hard-delete flow (ADR-021) and
    cascades the personal org with it.
  - Deleting a shared org is owner-only, uses its own
    `deleted_pending → hard-delete` lifecycle, and does **not** delete
    member accounts.
  - Audit events keep actor attribution even after membership is
    removed; `org_invitations.invited_by` / actor FK columns use
    `ON DELETE SET NULL` for that reason.

- **Stable error codes** (added to `pkg/api/errors.go`):

  | Code | HTTP | Meaning |
  |---|---|---|
  | `org_not_found` | 404 | slug does not resolve to an org the principal can see |
  | `org_slug_invalid` | 422 | slug fails the regex or shape check |
  | `org_slug_taken` | 409 | slug already in use |
  | `org_member_cap_exceeded` | 403 | org has reached `OrgMembersMax` for its plan |
  | `org_invitation_cap_exceeded` | 403 | org has reached `OrgPendingInvitationsMax` |
  | `org_role_forbidden` | 403 | authenticated member lacks the role for this action |
  | `org_already_member` | 409 | accepting account already has a membership in the org |
  | `org_invitation_invalid` | 410 | token unknown, consumed, or revoked |
  | `org_invitation_expired` | 410 | token past its `expires_at` |
  | `org_last_owner` | 409 | removing or demoting the only owner is refused |
  | `org_personal_immutable` | 409 | personal org cannot accept members or be deleted standalone |
  | `org_api_key_requires_org` | 409 | legacy API key needs to be re-bound to an org |

- **Plan / quota table changes (PR 1 placeholder, populated at PR 2):**

  `pkg/api/limits.go` gains two fields that PR 2 populates with derived
  values from the existing per-plan budget ladder:

  - `OrgMembersMax` — maximum active members per non-personal org
  - `OrgPendingInvitationsMax` — maximum pending invitations per
    non-personal org

  These fields are added in PR 1 with explicit per-plan `0` rows and a
  `TestOrgMembersLimits_DerivedFromLadder` test (renamed from the PR-1
  `ZeroUntilAuthorised` placeholder) that pins the populated values
  per plan and asserts Free + unknown-plan fail-closed at `0`. The
  per-plan ladder — Free 0/0, Hobby 10/5, Pro 50/25, Scale 200/100 —
  tracks the existing `KeysMax` ratio (PR #543) at 2× the plan's
  per-account app budget, with pending invitations at 1/2 of the
  member cap to bound the fast-accept botnet signature (default 7-day
  TTL per `pkg/state/...`).

  **Open follow-up:** reconcile these derived values against
  `ex44_faas_financial_model.xlsx` once the workbook is updated. If the
  workbook diverges, a sibling PR must reconcile the constant table
  and re-run `TestOrgMembersLimits_DerivedFromLadder` — the financial
  model remains the source of truth.

- **Rejected alternatives:**

  - *Header / session-selected active org.* Adding an `X-Active-Org`
    header or persisting an active org in the session envelope was
    rejected because cookies would permanently bind to one org even
    when a user belongs to many; selecting via URL keeps authorization,
    idempotency, logs, caches, and SDK calls explicit and verifiable.
  - *Permanent legacy account fallback.* Keeping account-owned
    resources indefinitely for existing customers would force every
    handler to branch on `account.OrgID IS NULL` forever. The
    automatic personal-org backfill removes that branch after one
    release.
  - *Pricing seats in this milestone.* Adding per-seat prices requires
    financial-model updates (Plan × Seats matrix, proration rules,
    provider subscription-item quantities) that are explicitly out of
    scope. PR 1 ships the limit fields but leaves the values at 0 until
    the workbook is updated.
  - *Merge IAM-1 / IAM-2 / IAM-3 work.* Scoped API keys, TOTP MFA,
    and session revocation were shipped ahead of orgs and remain
    forward-compatible; folding them into IAM-6 would force a
    redesign of every IAM primitive.

- **Out of scope (separate issues):**

  - Paid per-seat charging, proration, subscription quantities.
  - SAML / OIDC / Google Workspace / Okta / GitHub org SSO; email-
    domain auto-join.
  - Custom roles and user-defined permissions.
  - Multiple simultaneous owners (this milestone uses explicit
    ownership transfer).
  - Frontend org switcher / invitation / membership UI (handled in
    the extracted frontend repository).
  - Cross-org aggregate enterprise billing or consolidated invoices
    above a single org.

- **Personal-org downgrade (PR-7 design, code deferred to PR-9):**

  A customer on a paid shared org may want to dissolve the shared
  org back to their personal org on downgrade (issue #190 §"team-
  sale path"). The shape is settled at PR-7; the wire endpoint
  lands in PR-9 alongside the per-seat billing cut-over because
  both need dual-write cycles on the Stripe subscription-item
  quantities (the inverse of the personal-org backfill in PR 3).

  Four-step shape, gated by `authz.OrgActionTransferOwnership` so
  only the current owner can dissolve:

  1. **Transfer ownership.** If the owner wants to keep one
     collaborator on the personal org, transfer to that account
     first via `POST /v1/orgs/{slug}/transfer_ownership`. Otherwise
     proceed to step 2 — the dissolving owner becomes the personal-
     org member (they always were).
  2. **Remove non-owner members.** `DELETE /v1/orgs/{slug}/
     members/{user_id}` for every admin/developer/viewer/billing
     member. Owner-only (PR-4 role matrix). The cap-in-tx back-stop
     inside `RemoveOrgMember` is the load-bearing check — refusing
     a removal that would leave zero members surfaces as
     `org_last_owner` (409), which is the next step's tripwire.
  3. **Convert the shared org to a personal org.**
     `POST /v1/orgs/{slug}/convert_to_personal` (PR-9 wire). The
     handler:
     - Verifies the caller is the only remaining active member
       (single-owner + zero other roles). Surfaces 409
       `org_member_cap_exceeded` with the offending role if not.
     - Stamps `orgs.personal = true` + flips the slug to the
       caller's personal-org slug (already enforced by the PR-3
       backfill — no rename conflict).
     - Migrates any org-scoped API keys (PR-6) to account-scoped
       by clearing `org_id` (the keys stay live; the account's
       personal org is the new scope).
     - Migrates any Stripe subscription-item quantities to the
       account's personal-org plan (PR-9 cut-over).
     - Emits `org.converted_to_personal` (new audit kind, dotted
       convention per ADR-035).
  4. **Re-engage personal-org immutability.** `loadMutableOrgByMembership`
     (already in place from PR-5 at
     `cmd/apid/org_handler_helpers.go:101-122`) refuses any
     `PATCH /v1/orgs/{slug}` whose slug resolves to a personal
     org — same posture as the per-add gate. No new code needed;
     the immutability guard re-engages automatically because the
     row's `personal` flag is now true.

  **Why PR-9 and not PR-7:** PR-9 ships the per-seat pricing
  cut-over (PR-7's `seat_usage` endpoint is visibility-only — no
  billing change). The convert endpoint mutates Stripe
  subscription-item quantities, which only PR-9 has the dual-write
  scaffolding for. PR-7 lands the design + the audit-kind contract
  + the `cmd/apid/org_handler_helpers.go` cross-reference so a
  future contributor reading the helpers knows where the design
  lives.

  **Failure modes the convert handler must guard:**

  - Conversion with multiple members → 409 `org_member_cap_exceeded`
    ("the personal org caps at 1 active member; remove members
    first").
  - Conversion with pending invitations → 409
    `org_invitation_cap_exceeded` ("revoke pending invitations
    first"). Mirrors the per-add gate at PR-2.
  - Conversion with non-zero metered usage in the current period
    → 409 `org_conversion_window_active` (PR-9 introduces the
    invoice-cut posture: a metered period in flight cannot be
    folded into the personal-org subscription mid-stream).

  **Open follow-ups (filed as separate issues):** PR-9 implements
  the wire endpoint + the Stripe cut-over; PR-10 ships
  `ListEventsByOrg(ctx, orgID, limit)` for the dashboard's "this
  org's activity" tab so `org.converted_to_personal` shows up in
  the timeline (the partial jsonb index
  `migrations/00NNN_events_org_id_idx.sql` lands with PR-10).

- **Compatibility notes:**

  - All existing APIs continue to work during the dual window. Flat
    routes resolve to the caller's personal org only; they never guess
    among shared orgs.
  - The new path-scoped APIs are additive; SDKs grow typed methods
    (`pkg/api/client.go`) and the SDK coverage gate passes.
  - `apid` is still the sole writer to customer-intent tables;
    `schedd` still owns `instances`; `vmmd` still owns Firecracker.
    These ownership rules are not changed.

- **Consequences:**

  - **Stable codes (12).** `pkg/api/errors.go` gains 12 new
    RFC 7807 codes (`org_not_found`, `org_slug_invalid`,
    `org_slug_taken`, `org_member_cap_exceeded`,
    `org_invitation_cap_exceeded`, `org_role_forbidden`,
    `org_already_member`, `org_invitation_invalid`,
    `org_invitation_expired`, `org_last_owner`,
    `org_personal_immutable`, `org_api_key_requires_org`) and a
    shared `OrgSlugPattern` constant. Once shipped, the code
    strings cannot be renamed.
  - **Plan / quota table.** `pkg/api/limits.go` gains two fields
    per plan row (`OrgMembersMax`, `OrgPendingInvitationsMax`),
    populated at PR 2 from the existing per-plan budget ladder (see
    "Plan / quota table changes" above). PR 1 shipped 0/0 rows with
    fail-closed accessors (`Plan.OrgMembersMax()` /
    `Plan.OrgPendingInvitationsMax()`) and a guard test
    (`TestOrgMembersLimits_DerivedFromLadder`) that catches any
    future contributor who removes the field, the accessor, or
    regresses the per-plan row.
  - **Schema growth.** PR 2 introduces `orgs`, `org_memberships`,
    `org_invitations`, and nullable `org_id` columns on the
    tenant-root tables classified in
    `docs/iam-6-ownership-inventory.md`. Backfill happens in PR 3
    inside a single transaction per account. Contraction (NOT
    NULL on the columns) lands in PR 9 after at least two
    released clean dual-write cycles.
  - **Wire shape.** Cookie sessions remain account-bound; selecting
    an org is a per-request decision via the URL slug. The new
    path-scoped APIs land in PR 4–PR 5 and grow SDK methods
    (the SDK coverage map is updated in the same PR).
  - **Operator surface.** No new control-plane surface is introduced
    in this PR. `apid` is still the only customer-intent writer;
    `schedd` still owns `instances`; `vmmd` still owns Firecracker.
    The org authorization seam lands in PR 4 via a new
    `pkg/authz` package and `pkg/auth/middleware` grows a
    `LoadOrg` analogue of the existing `LoadApp` IDOR-safe
    helper.

- **Verification gates:** replay-safety on every migration; `make test`,
  `make lint`, `make spec-check`, `make sdk-check`, `make sqlc-check`,
  `make proto-check`, and the Postgres-backed e2e matrix listed in the
  implementation plan at `/Users/poyrazk/.claude/plans/lexical-roaming-candle.md`.
  VM-lifecycle code is unchanged; no metal re-run is required for this
  PR.

  **PR 1 explicit exemptions:** `make spec-check`, `make sdk-check`,
  `make sqlc-check`, and `make proto-check` are N/A for this PR —
  no protos, OpenAPI, SDK code, or SQL is touched. Future PRs in
  the staged rollout MUST re-enable each gate the first time it
  becomes load-bearing for that PR's surface (e.g. PR 4 flips
  `spec-check` on; PR 5 flips `sdk-check`; PR 2 flips
  `sqlc-check` and `proto-check`).

## Verification (post-merge, IAM hardening mega-PR)

The matrix and the route table gained two regression-guard tests
in the IAM hardening mega-PR so a future contributor cannot ship
a role-permission change without an explicit test edit:

- `pkg/authz/authorize_test.go::TestRoleMatrix_NoOrphanCells`
  walks every `(action, role)` cell in `allowRoleMatrix` and
  asserts the canonical `roleMatrixCells` table has a matching
  row. The original 9-action matrix was missing the PR-6 cells
  for `OrgActionCreateApiKey` / `OrgActionRevokeApiKey`; this
  test pins the full 11 × 5 = 55-cell surface. A future PR that
  adds an action (e.g. `OrgActionManageSSO`) is forced to extend
  `roleMatrixCells` or this test fails.

- `cmd/apid/server_org_authz_test.go::TestOrgRoutes_GatedByAuthorize`
  walks every `/v1/orgs/{slug}/*` pattern with a principal that
  is NOT a member of the seeded org and asserts the response is
  4xx. Mirrors `TestAllV1Routes_RequireAuthOrLimit` (which guards
  the authn surface) for the authz surface. Together the two
  tests cover both halves of spec §11's "every authenticated route
  must be wrapped" defence.

## Amendment 1 (2026-11-27): purging deleted shared organizations

The `deleted_pending → hard-delete` step above was never built: a deleted
shared organization kept its slug and stayed in every member's `org list`
forever (prod hunt r73: `audit-1001-org`, deleted 2026-09-30).

- **List.** `GET /v1/orgs` leaves out `deleted_pending` organizations;
  `?include_deleted=true` (CLI `gregale org list --all`) keeps them.
- **Purge.** apid sweeps hourly (`pkg/apid/orgpurge`). A shared organization
  whose deletion was requested more than `api.DeletedOrgPurgeAfter` (30
  days) ago takes the tombstone slug `purged-<first 24 hex of its id>`,
  which frees its old slug, and its open invitations are revoked. The sweep
  claims rows with `for update skip locked`, so several apids may run it.
- **Why a tombstone, not a row delete.** 46 tables reference `orgs`, 19 of
  them `ON DELETE RESTRICT` (retained billing, usage and audit records that
  keep their financial subject, ADR-202610171904). Deleting the row would be
  refused, or would cascade into records that must be retained. The row stays,
  `deleted_pending`, with its memberships kept for audit attribution; only the
  slug and pending invitations are released.
- **Reserved prefix.** `api.ValidateOrgSlug` refuses slugs starting with
  `purged-`. Personal organizations are untouched: they follow the
  account's ADR-021 erasure.
- The Free-organization limit still counts an organization deleted this UTC
  month (ADR-20261104211412); purging does not change that.

