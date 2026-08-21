# ADR-123 · Per-app ingress "Organization members only" (`apps.public_auth_mode = 'members_only'`)

- **Status:** Proposed
- **Date:** 2026-08-21
- **Decision:** Extend the closed `apps.public_auth_mode` enum
  (ADR-079, ADR-118, ADR-119) with a sixth value `members_only`.
  When set, every public request to the app's hostname must
  carry a valid `faas_sid` session cookie whose principal is an
  **active member** of `apps.org_id`. The gateway validates the
  cookie via the existing `pkg/auth/middleware.RequireSession`
  (no new cookie — the IAM-6 cookie is reused), then calls
  `pkg/authz.IsOrgMember(ctx, app.OrgID, principalID)` to gate on
  the `(org_id, account_id, removed_at IS NULL)` triple. The
  gate runs in `pkg/gateway/handler.go::applyIngressMembersOnly`
  immediately after `applyIngressInternalSvc` (ADR-119) and
  before `applyEdgeRuleIP`, so a cookie-or-membership failure
  short-circuits before any Firecracker wake. **SynthServer
  mirrors the same gate at all three call sites**
  (`handleSynthesize`, `handleInvocationDispatch`,
  `handleInvocationDispatchBatch`); cron traffic carries no
  human session, so the synth gate blocks cron-fired wakes to a
  `members_only` app.

## Context

The four canonical ingress postures a customer expects from a
modern PaaS (table lifted from
[[118-app-public-auth-ip-allowlist]] §Context and amended in
[[119-app-public-auth-internal-only]] §Context):

| Posture                              | Today                          |
|--------------------------------------|--------------------------------|
| Public (open)                        | ✅ `public_auth_mode=open` (ADR-079) |
| Organization members only            | ⚠️ partial — IAM-6 cookie/org session (ADR-061), but no per-app `public_auth_mode` value forces it |
| Selected IP ranges                   | ✅ `public_auth_mode=ip_allowlist` (PR #999, ADR-118) |
| Internal Gregale services only       | ✅ `public_auth_mode=internal_only` (PR #1009, ADR-119) |

ADR-118 line 239-247 explicitly carved the second bullet out as
"a separate PR" once the foundational IAM-6 cookie layer landed:

> *"The org-membership ingress gate is also out of scope —
> ADR-061 already provides org-bound cookie sessions, but a
> unified `members_only` public-auth mode is a separate PR."*

ADR-119 line 313-326 re-listed it as future-work after shipping
`internal_only`:

> *"`members_only` mode (the bullet-2 unification): still
> covered by the IAM-6 cookie layer; out of scope here."*

This ADR closes the second bullet. The IAM-6 cookie layer
(ADR-061) is the load-bearing foundation: every Gregale web
session is already bound to a `principal.Account`, and every
account can be a member of one or more orgs via the
`org_memberships` table (migration 00099, the personal-org +
shared-orgs split). What's missing is the per-app **gate** that
*forces* the cookie to be present *and* the principal to be a
member of the **app's** org. Today a customer who wants
"members of org-X only" must do it via edge rules + custom
JWTs (the D21 `kind='jwt'` rule) or via `bearer` mode + their
own gateway in front. The first scales poorly (each app
needs a custom JWKS), the second punts the problem to the
customer.

The shape of the fix is a sixth `public_auth_mode` value
`members_only` plus a one-line gate that consults
`pkg/authz.IsOrgMember(ctx, app.OrgID, principalID)`. Reuses
the IAM-6 cookie path entirely — no new cookie, no new token,
no new wiring on the dashboard auth path. The cookie side
already does the AEAD-decrypt + live-row cross-check + binding-
hash verification (the stolen-cookie defense at
`pkg/auth/middleware.RequireSession`); the gate only adds the
membership predicate.

### Closest precedents (which this ADR explicitly distinguishes from)

- **IAM-6 cookie/org session** ([[061-iam-6-cookie-org-session]]):
  the foundation. This ADR is the *per-app gate* that sits on
  top of the IAM-6 cookie. ADR-061 stamped the
  `principal.Membership` field based on the
  `X-Active-Org` / `?org=` header; **this ADR does NOT use
  that header** — `IsOrgMember` looks up directly against
  `apps.org_id`. A request that hits the `members_only` gate
  cannot game the membership check by sending a forged
  `X-Active-Org=<target-org>`. See the *`X-Active-Org` header
  interference* note in `pkg/authz/is_org_member.go::IsOrgMember`.
- **Per-app JWT edge rule** ([[091-connection-aware-execution]]
  D21, the `kind='jwt'` edge rule): customer-issued JWT
  verified against the customer's JWKS endpoint. **Different
  trust model**: customer controls the key. For `members_only`,
  Gregale controls the cookie and the membership predicate; the
  customer cannot mint themselves a session for another account
  even if they wanted to.
- **`internal_only` mode** ([[119-app-public-auth-internal-only]]):
  the closest sibling in the closed enum. Same gate shape
  (a per-app ingress gate that runs after `applyIngressIPAllowlist`
  and before `applyEdgeRuleIP`), same plan-tier
  (Hobby+-by-default), same audit + metric shape
  (`gateway_edge_rule_match_total{kind="ingress_members",…}` +
  `edge_rule.ingress_members_blocked` audit row), same
  SynthServer mirror, same fail-closed lookup posture. The
  *differences* are the cookie-vs-JWT trust model and the
  401-vs-403 split: a stale/revoked cookie is 401 (cookie
  authn failed), a valid-cookie-but-not-a-member is 403
  (authn passed, authz denied).
- **`bearer` mode** (ADR-079): the Hobby+-tier plan gate
  precedent that motivates Hobby+ for `members_only` (per
  the plan-tier table in `pkg/api/limits.go::Limits`).

## Decision

### Closed enum widening (no new column)

```sql
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_public_auth_mode_chk;
ALTER TABLE apps ADD CONSTRAINT apps_public_auth_mode_chk
  CHECK (public_auth_mode IN ('open','bearer','basic','ip_allowlist','internal_only','members_only'));
```

The session lives on the **request** (the `faas_sid` cookie,
already issued by `pkg/auth/middleware.IssueSession`). The
membership predicate lives in Postgres (the
`(org_id, account_id, removed_at IS NULL)` PK on
`org_memberships`). The app row pins the policy that says
"this app is reachable only by members of *my* org".

### `pkg/authz.IsOrgMember` — the new membership predicate

```go
// pkg/authz/is_org_member.go (NEW)
func IsOrgMember(ctx context.Context, pool *pgxpool.Pool,
    orgID, accountID string) (bool, error) {
    if orgID == "" || accountID == "" {
        return false, nil
    }
    var ok bool
    err := pool.QueryRow(ctx,
        `SELECT EXISTS(
             SELECT 1 FROM org_memberships
             WHERE org_id = $1
               AND account_id = $2
               AND removed_at IS NULL
         )`, orgID, accountID).Scan(&ok)
    if err != nil {
        return false, errors.Join(ErrMembershipLookup, err)
    }
    return ok, nil
}
```

Three deliberate design choices, each with a lesson pinned to
a comment in the source file:

1. **`SELECT EXISTS(SELECT 1 …)` over `QueryRow` + `pgx.ErrNoRows`
   handling.** `EXISTS` always returns exactly one row with a
   bool — the implicit-type-resolution `Scan(&ok)` is total.
   No `ErrNoRows` branch needed. Same shape as
   `pkg/auth/middleware.LookupSession`'s `EXISTS` check for the
   live session row.
2. **`removed_at IS NULL` in the predicate, not in a post-filter.**
   Removed members are a first-class SQL concept (the soft-delete
   pattern `org_memberships` uses for auditability) and the PK
   index already covers `(org_id, account_id)` — the soft-delete
   filter is a single-btree-leaf equality lookup. O(log n).
3. **Fail-closed DB-error posture**: every DB error returns
   `(false, ErrMembershipLookup)`. The gate at
   `applyIngressMembersOnly` pivots on `errors.Is(err,
   ErrMembershipLookup)` to surface a controlled 401 + audit
   `reason='lookup_error'` rather than a 500 from the unwrapped
   pgx error. Mirror of ADR-119 round-2 fail-closed lesson at
   `cmd/schedd/internal_svc_minter.go:6`. `errors.Join(ErrMembershipLookup, err)`
   is the wrap shape — `errors.Is` walks the chain regardless
   of which side the caller unwraps, and the operator's log
   keeps the pgx detail (the audit payload does NOT — it
   carries `actor_account_id` + `app_id` + `reason` only).

The package-local `OrgMemberChecker` interface
(`IsOrgMember(ctx, orgID, accountID) (bool, error)`) is the
narrow bridge `pkg/gateway` consumes — `pkg/gateway` does NOT
import `pgxpool` directly, matching the L300-303 boundary. The
production wiring at `cmd/gatewayd-internal` wraps
`pkg/authz.PoolOrgMemberChecker(pool)`. Unit tests at
`pkg/gateway/public_auth_members_only_test.go` use a stub that
returns canned `(bool, error)` pairs.

### Plan ladder: Hobby + Pro + Scale (mirror `bearer`)

| Plan | Allowed? | Error code on PATCH |
|------|----------|---------------------|
| Free     | ❌ | `plan_public_auth_members_only_not_allowed` (402) |
| Hobby    | ✅ | — |
| Pro      | ✅ | — |
| Scale    | ✅ | — |

Encoded at `pkg/api/limits.go::Limits.PublicAuthMembersOnlyAllowed`
(next to `PublicAuthBearerAllowed` / `PublicAuthIPAllowlistAllowed`),
with the Hobby/Pro/Scale plan rows setting the field to `true`
and Free staying at the zero value (`false`). The Plan gate at
`cmd/apid/handlers_ext.go::PublicAuthPatch` returns
`ErrPlanPublicAuthMembersOnlyNotAllowed(acct.Plan)` (402
Payment Required) for a PATCH that targets `members_only` on a
Free plan. The 422-supersedes-402 invariant from ADR-079
(`validation_failed` for an unknown mode value) still holds —
the closed-enum check fires first, so a Free customer who
PATCHes `mode='bogus'` gets 422, not 402.

Rationale for Hobby+ (not Pro+): Hobby already unlocks the
`OrgMembersMax` ladder via ADR-061 line 174; `members_only` on
a Hobby personal-org app is functionally `bearer` with the
same account (every personal org has exactly 1 member, the
account themself). Hobby+ matches the `bearer` tier
deliberately — both are "the app is reachable only by an
authenticated user with a Gregale session", and Free has no
non-personal-org footprint to gate on.

### Gate order

```
applyIngressIPAllowlist (ADR-118, cheap, no SQL)
  ↓
applyIngressInternalSvc (ADR-119, AEAD verify, no SQL)
  ↓
applyIngressMembersOnly (this ADR, 1 SQL on hit)        ← NEW
  ↓
applyEdgeRuleIP (the existing geo/IP kind=ip gate)
  ↓
... downstream public_auth / wake / require_authn ...
```

`applyIngressMembersOnly` sits between `applyIngressInternalSvc`
and `applyEdgeRuleIP` for the same reason its two siblings do:
a denied request never wakes a Firecracker microVM. The
membership-lookup SQL is O(log n) on the PK
`(org_id, account_id)` index — the only way to make it slower
is to add an extra `removed_at` filter at the wrong layer.

### Trust chain: RequireSession precedes the gate

The cookie is validated by `pkg/auth/middleware.RequireSession`
**before** `applyIngressMembersOnly` ever sees the request
(it's upstream in the public-daemon hop's middleware chain).
That means by the time the gate runs:

- The cookie envelope is AEAD-decrypted (a stolen/forged cookie
  is rejected at the AEAD layer).
- The session row is live-row-cross-checked against
  `pkg/auth/sessions` (a revoked session is auto-revoked by
  the middleware at L648-741, and the request is 401'd before
  reaching the gate).
- The cookie's binding-hash is matched against the
  User-Agent + IP quartet (a stolen cookie from a different
  device fingerprint is auto-revoked the same way — the
  ADR-076 binding-mismatch defense).

The `members_only` gate only adds the **membership** check on
top. It does not re-validate the cookie itself. This is
deliberate: a second-cookie-validation pass would be a
fingerprint oracle for "this cookie passed authn but failed
authz" vs "this cookie was always going to fail authn", and
the authn-vs-authz split is the whole point of the 401-vs-403
distinction.

### 401 vs 403 — the customer-facing wire surface

| Failure mode | Status | Reason code | Audit row |
|--------------|--------|-------------|-----------|
| No cookie / revoked / binding-mismatch (handled by RequireSession upstream; rare at the gate) | 401 `Unauthorized` | `no_cookie` | `edge_rule.ingress_members_blocked{reason="no_cookie"}` |
| Membership row absent (account never joined this org) | 403 `Forbidden` | `not_member` | `edge_rule.ingress_members_blocked{reason="not_member"}` |
| Membership row soft-deleted (`removed_at IS NOT NULL`) | 403 `Forbidden` | `removed_member` | `edge_rule.ingress_members_blocked{reason="removed_member"}` |
| Membership DB error | 401 `Unauthorized` (fail-closed) | `lookup_error` | `edge_rule.ingress_members_blocked{reason="lookup_error"}` |
| Operator misconfig (no checker wired, empty `apps.org_id`) | 500 `Internal` | `operator_error` | `edge_rule.ingress_members_failed` |

The 401-vs-403 split lets the dashboard render two distinct
CTAs: "you forgot to log in" (401) vs "you are logged in but
not a member of this org" (403). The customer-facing
`Problem.detail` does NOT distinguish the cookie-side
sub-reasons (`no_cookie` vs revoked vs stolen) so the gate
does not become a fingerprint oracle. The audit row carries
the precise reason — the operator's pipeline pivots on it
without re-running through the emitter.

### Audit redaction invariant

The cookie envelope / session id MUST NEVER appear in an audit
payload (carry-over from PR #999 + PR #1009, codified in
`CLAUDE.md` §Conventions: "Never log secret values; env secrets
are sealed at rest (gap G2 lean, §17)"). The `cookiePrincipalExtractor`
returns only the `account_id` (a UUID, not a cookie value); only
that + `app_id` + `from_host` + a short reason enum flow into
the audit row. Pin test:
`TestApplyIngressMembersOnly_AuditDoesNotEchoCookie` at
`pkg/gateway/public_auth_members_only_test.go` substring-checks
that `faas_sid=<value>` never appears in the audit JSON.

### SynthServer mirror — block cron at the gate

Cron traffic carries no human session, so the synth-side
`applyIngressMembersOnly` denies every cron wake to a
`members_only` app. The wake never fires. Mirrors the design
gap surfaced during the ADR-119 PR-A build (`schedd` cron
bypasses `Handler.ServeHTTP` entirely via
`pkg/gateway/synth.go:handleSynthesize`; the gate must cover
both surfaces — see the round-2 lesson at
`internal-only-gate-bypass-via-synth-handler`). The synth
mirror is wired at all three call sites:

- `handleSynthesize` (cron-fired wake creation)
- `handleInvocationDispatch` (single-instance dispatch)
- `handleInvocationDispatchBatch` (batch dispatch)

Customers who want cron-receivable member-gated apps must use
`open` or `internal_only`. A future `members_only_for_cron`
mode (out of scope) would model service-account membership, but
that's a §17 follow-on, not v1.0.

### Plan + tier + cost rationale

`members_only` adds 1 SQL (`SELECT EXISTS` on the
`(org_id, account_id, removed_at IS NULL)` triple) per denied
*or* allowed request that passes the prior gates. That's the
same per-request budget as the `bearer` mode's session-row
read at `pkg/auth/middleware.LookupSession` (the
`pkg/auth/sessions` table) — the two reads are designed to
compose, and the customer's per-app concurrency limit caps
the worst case regardless. The PK index makes the lookup
O(log n) — no fan-out, no per-row soft-delete sweep.

## Schema materialization

Migration `00347_apps_public_auth_members_only.sql` widens
the `apps_public_auth_mode_chk` CHECK constraint by
DROP-then-ADD (per the trigger-replay-safety DROP-before-CREATE
precedent at `trigger-replay-safety-drop-before-create`). The
mirror at `schema.sql` is updated in lock-step — the canonical
materialization gate (`make sqlc-check`) re-derives both from
the migration history on every CI run.

The migration's test file
(`migrations/00347_apps_public_auth_members_only_test.go`)
pins seven scenarios: `ApplyThrough` (round-trip the new
value), `EnumAccepts` (closed-set still rejects unknown
values), `RoundTrip` (mode flip `open → members_only →
open` is total), `EnumRejectsUnknown` (the 23505-era
unknown-mode path), `EnumStillAcceptsExisting` (the
five-prior-values are not lost), `DownGrade_Narrows` (the
DOWN migration narrows the CHECK back to the five-value
pre-ADR-123 set), `AfterDown` (the post-DOWN row rejects
`members_only` on a fresh INSERT).

## Verification (test coverage)

- `pkg/authz/is_org_member_test.go` — 5 table-driven sub-tests
  (`ActiveMember`, `RemovedMember`, `NoRow`, `DBError`
  fail-closed, `EmptyInputs` × 3). Uses `pgtest.Open + db.MigrateUp`
  for the DB-backed paths. All five pin the (bool, error)
  contract + the `ErrMembershipLookup` sentinel.
- `pkg/gateway/public_auth_members_only_test.go` — 8 sub-tests
  (`ActiveMember` pass-through, `NoPrincipal` 401, `NonMember`
  403, `LookupError` fail-closed 401, `EmptyOrgID` 500
  misconfig, `OtherMode` no-op, `WiringIncomplete` 500
  misconfig, `DBBacked` end-to-end happy path against
  `pkg/authz.PoolOrgMemberChecker`).
- `pkg/gateway/synth_members_only_test.go` (NEW) — mirrors
  the gate at the three SynthServer call sites
  (`TestApplySynthIngressMembersOnly_BlocksCron`,
  `TestApplySynthIngressMembersOnly_ActiveMemberPasses`,
  `TestApplySynthIngressMembersOnly_LookupErrorFailsClosed`).
- `cmd/apid/handlers_public_auth_test.go::TestPublicAuthPatch_MembersOnlyPlanGate`
  — 5 sub-tests pinning the Hobby+/Free matrix
  (`free_returns_402`, `hobby_returns_200`, `pro_returns_200`,
  `scale_returns_200`, `closed_enum_supersedes_plan_gate`).
- `pkg/api/public_auth_constants_test.go` + `pkg/gateway/handler_public_auth_constants_test.go`
  — drift guards pin the three-place constant mirror
  (pkg/api ↔ pkg/state ↔ pkg/gateway).
- `migrations/00347_apps_public_auth_members_only_test.go` —
  7 sub-tests pinning the closed-enum migration contract.
- `TestMigrationsContiguous` — pre-existing gate that catches
  slot-fence drift on the 00347 slot.

## Future work (deliberately out of scope for this ADR)

- **Per-role granularity** (`owner_only`, `admin_only`, etc.)
  — first PR ships the "any active member" semantic. Customers
  who want finer-grained gating can layer it on per-deployment
  or via edge rules (ADR-119 line 321-326 precedent). Adding
  `role` as a mode dimension would re-open the closed-enum
  precedent (ADR-079: one mode per app row, full stop).
- **`members_only` + `internal_only` composition** — the
  closed enum is a single discriminator. A future PR could
  model `members_only + ip_allowlist` as a parallel
  ip+cookie gate, but the closed-enum precedent prefers one
  mode per app row.
- **Cross-org member access** — every account has a personal
  org (ADR-061 line 41). Members of the personal org = the
  account themself. Members of shared orgs are scoped to that
  org. No cross-org synthesis.
- **mTLS at unix-socket layer** — explicitly deferred per
  ADR-052. The cookie-on-Authorization-header is the v1.0
  trust model for `members_only` ingress.
- **Cookie revocation propagation latency** —
  `pkg/auth/middleware.RequireSession` does a live-row
  cross-check (L648-741) on every request. A revoked cookie
  401s within cache TTL; the gate inherits this. Future
  hardening could add a per-account short-circuit cache, but
  not in this PR.
- **`members_only` for cron receiver** — cron has no human
  session, so the gate blocks. Customers who want
  cron-receivable member-gated apps must use `open` or
  `internal_only`.
- **Per-membership TTL** — the gate does not currently check
  the membership's `joined_at` (e.g. "members for at least
  7 days"). A future hardening could add a `stale_after`
  field on `org_memberships` and a `JOINED_AT < $3` clause.

## Deployment requirements (operator-side)

None. The new gate is a code-only change — no new env vars,
no new SQL indexes (the existing PK on
`org_memberships(org_id, account_id)` covers the lookup), no
new secret material, no new audit pipeline consumer. The
cookie-side wiring is the existing `pkg/auth/middleware`
chain that already runs on the public daemon; the
membership-side wiring is `pgxpool` which already runs on
the internal daemon.

The four package-local surfaces the operator sees:

- `pkg/authz.PoolOrgMemberChecker(pool)` — the prod wiring
  for `WithMembersOnlyChecker`.
- `cmd/gatewayd-internal/auth_principal_adapter.go` (NEW) —
  the prod wiring for `WithMembersOnlyPrincipalExtractor`,
  which imports `pkg/auth/middleware.PrincipalFrom` to
  resolve the cookie-side `account_id` from `r.Context()`.
- `cmd/gatewayd-internal/run.go` — `WithMembersOnlyChecker`
  + `WithSynthMembersOnlyChecker` chained onto the
  gateway-side and synth-side server constructors.
- `pkg/gateway/metrics.go` (L1051) — the kind pre-instantiation
  slice extended with `"ingress_members"` (closed set: now
  `ingress_ip` + `ingress_internal` + `ingress_geo` +
  `ingress_throttle` + `ingress_members`).

## References

- [[061-iam-6-cookie-org-session]] — IAM-6 foundation; the
  `pkg/auth/middleware` cookie path this gate sits on top of.
- [[079-app-public-auth-closed-enum]] — the closed-enum
  precedent (one mode per app row).
- [[091-connection-aware-execution]] D21 — the `kind='jwt'`
  edge rule (different trust model, customer-controlled keys).
- [[118-app-public-auth-ip-allowlist]] — the most-recent
  closed-enum widening; the `ip_allowlist` gate is the closest
  per-app-gate precedent.
- [[119-app-public-auth-internal-only]] — the most-recent
  closed-enum widening; the `internal_only` gate is the
  closest in trust-model terms.
- Migration `00099_org_memberships.sql` — the
  `org_memberships` table + PK `(org_id, account_id)` + the
  `removed_at IS NULL` soft-delete pattern.
- Migration `00347_apps_public_auth_members_only.sql` — the
  ADR-123 widening.
- `pkg/auth/middleware/RequireSession` — the cookie side
  (AEAD-decrypt + live-row cross-check + binding-hash verify).
- `pkg/gateway/handler.go::applyIngressMembersOnly` — the
  new gate (between `applyIngressInternalSvc` and
  `applyEdgeRuleIP`).
- `pkg/gateway/synth_members_only.go::applyIngressMembersOnly`
  — the synth-side mirror (cron path).

## Cited precedents (file paths)

- `pkg/auth/middleware/middleware.go:RequireSession` — cookie
  trust chain (AEAD, live-row, binding-hash).
- `pkg/gateway/handler.go:applyIngressInternalSvc` — the
  sibling-gate pattern this ADR mirrors.
- `pkg/gateway/handler.go:applyIngressIPAllowlist` — the
  earlier sibling-gate pattern (ADR-118).
- `pkg/authz/is_org_member.go:IsOrgMember` — the new
  membership predicate.
- `pkg/authz/errors.go:ErrMembershipLookup` — the
  fail-closed sentinel.
- `cmd/schedd/internal_svc_minter.go:6` — ADR-119 round-2
  fail-closed lookup lesson (the canonical reference for the
  `errors.Is(err, sentinel) → 401 + audit lookup_error` pattern).
- `pkg/gateway/metrics.go:1051` — the kind pre-instantiation
  loop; extended to include `ingress_members` so the §12
  dashboard renders zero-valued rows from first scrape.
- `pkg/api/limits.go::PublicAuthMembersOnlyAllowed` — the
  per-plan gate field.
- `pkg/api/errors.go:CodePlanPublicAuthMembersOnlyNotAllowed`
  — the 402 wire code.
- `cmd/gatewayd-internal/auth_principal_adapter.go` — the
  cmd-side cookie → account-id resolver (the only NEW
  cmd-side file; everything else is per-package addition).
