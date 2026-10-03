# ADR-118 · Per-app ingress IP allowlist (`apps.public_auth_mode = 'ip_allowlist'`)

- **Status:** Proposed
- **Date:** 2026-08-20
- **Decision:** Extend the closed `apps.public_auth_mode` enum
  (ADR-079) with a fourth value `ip_allowlist`. When set, every
  public request to the app's hostname must originate from a
  client IP inside `apps.public_auth_ip_allowlist cidr[]` —
  anything else 403s. Empty list = no rule (default-off, current
  behaviour preserved). The gate runs at the request layer in
  `pkg/gateway/handler.go::applyIngressIPAllowlist`, immediately
  before `applyEdgeRuleIP`, so an IP-blocked request never wakes
  a Firecracker microVM.

## Context

Gregale's public-URL access controls today cover two of the four
canonical postures a customer expects from a modern PaaS:

| Posture                              | Today                  |
|--------------------------------------|------------------------|
| Public (open)                        | ✅ `public_auth_mode=open` (ADR-079) |
| Organization members only            | ✅ IAM-6 cookie/org session (ADR-061) |
| Selected IP ranges                   | ❌ **missing** — ADR-079 reserved the future enum value `ip_allowlist` but never implemented it |
| Internal Gregale services only       | ❌ **out of scope here** — separate ADR |

This PR closes the third bullet. ADR-079 already reserved the
enum value as a future extension point
([[079-per-app-public-auth]] §Context, lines 27 + 66). The
per-app egress allowlist
([[031-app-egress-allowlist]] / [[033-app-egress-allowlist-v6]])
ships the schema + trigger pattern we can borrow verbatim; the
`kind='ip'` edge rule ([[091-connection-aware-execution]] D21 +
ADR-089 metrics convention) ships the runtime gate pattern we
can borrow verbatim. Both precedents exist; the gap is just
wiring them together at the public-auth layer.

## Decision

### Closed enum widening

```sql
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_public_auth_mode_check;
ALTER TABLE apps ADD CONSTRAINT apps_public_auth_mode_check
  CHECK (public_auth_mode IN ('open','bearer','basic','ip_allowlist'));
```

`apps.public_auth_ip_allowlist cidr[] NOT NULL DEFAULT '{}'` —
mirror the egress column at `migrations/00029_app_egress_allowlist.sql:22`.

### Trigger (not CHECK) for per-element guards

`migrations/00308_apps_public_auth_ip_allowlist.sql` mirrors
`migrations/00033_app_egress_allowlist_v6.sql:19-64` verbatim,
rejecting:

1. `family(c) NOT IN (4, 6)` — defensive; `cidr` can't hold
   anything else today, but the guard is documented at the
   trigger for posterity.
2. `masklen(c) = 0` — no all-of-internet. Both `0.0.0.0/0` and
   `::/0` rejected.

Empty array short-circuits at the top of the function — the
canonical "no rule" state is never validated. `errcode = '23514'`
(`check_violation`) + `constraint = 'apps_public_auth_ip_allowlist_cidr'`
so callers can match on `pgErr.ConstraintName`.

**Why a trigger, not a CHECK** — Postgres CHECK can't reference
aggregates over an array element
(`bool_and(family(cidr) IN (4,6))` is a parse error). The egress
path takes the same trigger pattern; we mirror it exactly.

### Plan ladder (paid-only, mirror egress)

| Plan  | `PublicAuthIPAllowlistAllowed` | `PublicAuthIPAllowlistMaxEntries` |
|-------|--------------------------------|-----------------------------------|
| Free  | `false` (absent)               | `0` (absent)                      |
| Hobby | `false` (absent)               | `0` (absent)                      |
| Pro   | `true`                         | `16`                              |
| Scale | `true`                         | `64`                              |

`pkg/api/limits.go` accessors fail-closed on unknown plan (return
`false` / `0`), matching the `EgressAllowlistAllowed` /
`EgressAllowlistMaxSize` pair at `limits.go:3110,3124`.

apid's PATCH handler returns 403
`plan_public_auth_ip_allowlist_not_allowed` for Free/Hobby (new
error code in `pkg/api/errors.go`), and 400
`public_auth_ip_allowlist_too_long` for Pro/Scale exceeding the
per-plan entry cap.

**Why paid-only** — egress allowlist takes the same posture
(`Free 0/0, Hobby 0/0, Pro 16, Scale 64`). Free/Hobby customers
have the abuse-floor posture satisfied by edge rules
(`kind='ip'`), which are available on every plan. The per-app
allowlist is the Pro+ feature for SaaS-scale egress hygiene,
where every CIDR is a deliberate policy decision; we don't
want a Free-tier customer with a single shared-host IP pinning
the whole platform.

### Three-place constant mirror (drift-guarded)

The drift guard test at `pkg/api/public_auth_constants_test.go:32-50`
pins the three surfaces equal. We extend it to require the
new constant in all three:

| Surface                            | Constant                              |
|------------------------------------|---------------------------------------|
| `pkg/api/public_auth.go`           | `AppPublicAuthModeIPAllowlist = "ip_allowlist"` (exported) + `AppPublicAuthIPAllowlistMaxEntries` (int) |
| `pkg/state/types.go`               | `PublicAuthModeIPAllowlist = "ip_allowlist"` |
| `pkg/gateway/handler.go`           | `publicAuthModeIPAllowlist = "ip_allowlist"` (gateway-local lowercase) |

### Runtime gate (the part that's load-bearing)

A new `applyIngressIPAllowlist` method on `*Handler` runs at
`pkg/gateway/handler.go:4260-4271`, **before** the
`applyEdgeRuleIP` block. The chain order becomes:

```
ingress-ip → edge-rule-ip → edge-rule-geo → require-authn
  → public-auth → sidecar → wake
```

An IP-blocked request never wakes a Firecracker — same invariant
as the geo gate's comment at `handler.go:4260`.

The gate is a near-verbatim copy of `applyEdgeRuleIP`
(`handler.go:1995-2072`), with two semantic differences:

1. **Empty allowlist + `ip_allowlist` mode is a 500, not a 403.**
   A misconfigured app (mode set, no CIDRs) is loud — the
   operator sees a 500 immediately, not a silent 403 on every
   request. `applyEdgeRuleIP`'s "allow empty = pass-through"
   posture is correct for edge rules (no rule is a valid
   posture); for the public-auth gate, "mode set, no CIDRs" is
   an operator error.

2. **No separate `matched` audit event.** Pass-throughs are
   silent; only denies emit. Mirrors the geo gate
   (`handler.go:2980-3076`) which also drops the matched frame.

The trust chain is identical to `applyEdgeRuleIP`:
`clientIPFromTrustedXFF(r)` fails closed with 403
`caller_ip_forged` on 0 or >1 XFF entries. Any ingress IP
feature must reuse this — gatewayd-public already overwrites
XFF with the customer's actual IP at
`pkg/gateway/internal_proxy.go:286-289`, so the unix-socket
peer inside gatewayd-internal is irrelevant.

### Audit + metric codes (kind-outcome template)

Follow the existing
`"edge_rule." + kind + "_" + outcome` convention:

| Code                          | When                                              |
|-------------------------------|---------------------------------------------------|
| `edge_rule.ingress_ip_blocked`| Trusted client IP not in CIDR allowlist          |
| `edge_rule.ingress_ip_forged` | XFF chain has 0 or >1 entries (fail-closed)      |

Counter: `gateway_edge_rule_match_total{kind="ingress_ip", outcome="blocked"}`
— reuses the existing `edgeRuleMatch` `CounterVec`. The closed
kind set at `pkg/gateway/metrics.go:1043-1055` is extended from
`{route, rewrite, redirect, headers, cors, jwt, ip, validate,
limit, maintenance, geo, throttle}` to `…, ingress_ip`. **This
extension is load-bearing** — the dashboard chip "edge rule
apply rate" depends on every kind being pre-instantiated so idle
boxes render zero-valued, not "no data" (ADR-089).

### Audit redaction (the load-bearing invariant)

`cmd/apid/handlers_ext.go::app.public_auth_changed` carries
`public_auth_ip_allowlist_entry_count: int` (the count, never
the CIDR strings). The redaction test
`cmd/apid/handlers_public_auth_test.go::TestPublicAuthPatch_AuditEmitsWithRedaction`
is mirrored as
`TestPublicAuthPatch_IPAllowlistAuditEmitsEntryCount` — scans
raw audit JSON for `"10.0.0.0/8"` and asserts no CIDR string
ever leaks. This invariant is the same one basic-auth honours
(secret values never logged; only `has_basic_creds: bool` is
emitted, see `handlers_ext.go:1053-1079`).

### Live update

**No new wire RPC.** `cmd/apid/handlers_ext.go:878-879` already
emits `db.NotifyAppChanged` on every PATCH, and
`cmd/gatewayd-internal/backend.go:441-464` already calls
`inv.ResetApp(payload)` for the per-app drop — the cached
`PublicAuthConfig` is invalidated on any patch. The next request
re-hydrates with the new CIDR list.

This is fundamentally simpler than egress — ingress lives at
the request layer, not inside a guest netns. Egress required the
`EgressDriftSubscriber` fan-out to running instances
(`pkg/sched/egress_drift.go:3-31`) because the ruleset is baked
into a per-netns nftables table. Ingress has no such state — the
allowlist is re-read on every request from the per-app cache.

## Consequences

### Positive

- Closes the third bullet of the canonical 4-posture ingress
  model. Free/Hobby still have `kind='ip'` edge rules; Pro/Scale
  get the simpler per-app primitive.
- Single migration; the trigger mirrors an existing
  (`00033`) one verbatim — zero new SQL patterns.
- No live-drift fan-out, no new cache, no new wire RPC — the
  feature is structurally simpler than egress allowlist.
- Three-place constant drift guard extends naturally — the
  closed enum widening lands in one transaction across all
  surfaces.
- The 500-on-misconfig invariant is a deliberate
  operator-loudness improvement over the kind='ip' edge rule's
  pass-through posture.

### Negative

- One new migration slot (`00308` — slot walks past PR #997's
  `00304-00307` fence pattern, see
  [[migration-gates-collision-and-replay]] /
  [[cross-pr-slot-precheck-pr-867-collision-2026-08-13]]).
- The 500-on-misconfig posture means a customer who enables
  `ip_allowlist` mode and forgets to add CIDRs sees every
  request 500. Documented in the PATCH handler error and the
  apid error explanation table; the alternative (silent pass)
  is worse — a customer who enabled the mode without realising
  the empty list is meaningless would never notice.
- `pkg/gateway/metrics.go:1043-1055` closed-set extension is a
  dashboard contract — adding a kind is a forever-pinned
  Prometheus label value. Future operators must keep the
  pre-instantiation list in sync when they add new gate kinds.
- No `ingress_ip_matched` audit event — a customer who wants
  per-request allow/deny audit coverage must layer an edge rule
  on top. Acceptable trade for the simplicity of "no audit
  noise on pass-through".

### Neutral

- `internal_only` mode (the fourth bullet) is **deliberately
  out of scope** — it needs a Gregale service-token signing
  key (`FAAS_INTERNAL_SVC_KEY`), a separate `aud='gregale.internal'`
  mint path, and a per-service allowlist. A future ADR will
  add it as a fifth enum value.
- The org-membership ingress gate is also out of scope —
  ADR-061 already provides org-bound cookie sessions, but a
  unified `members_only` public-auth mode is a separate PR.
- No SDK regen — `pkg/api/sse.go` already preserves `event:`
  names verbatim, and no public wire types change.

## Reuse (don't reinvent)

- `pkg/gateway/handler.go:1995` `applyEdgeRuleIP` — runtime
  pattern. Mirror.
- `pkg/gateway/handler.go:2980` `applyEdgeRuleGeo` — audit /
  metric / fail-closed shape.
- `pkg/gateway/handler.go:3319` `clientIPFromTrustedXFF` —
  trust chain. Reuse verbatim.
- `migrations/00033_app_egress_allowlist_v6.sql:19-64` — trigger
  body. Mirror verbatim.
- `migrations/00254_edge_rules_kind_budget.sql:67-71` —
  DROP+ADD CHECK widening. Mirror.
- `cmd/apid/handlers_ext.go:100-195` — egress allowlist
  parse + plan gate + size gate + v4-mapped-v6 canonicalization
  + dedup. Mirror for ingress.
- `cmd/apid/handlers_ext.go:1053-1079` — `app.public_auth_changed`
  audit emitter with `has_basic_creds` redaction invariant.
  Extend with `public_auth_ip_allowlist_entry_count`.
- `pkg/api/public_auth_constants_test.go:32-50` — drift guard
  test. Extend with the new constant.
- `pkg/api/limits.go:662-677` `EgressAllowlistAllowed` /
  `EgressAllowlistMaxSize` — limits field pair. Mirror.
- `pkg/api/limits.go:3110,3124` — accessor pattern.
  Mirror.
- `pkg/gateway/metrics.go:1043-1055` — closed-set
  pre-instantiation. Extend.
- `cmd/apid/handlers_public_auth_test.go:260-335`
  `TestPublicAuthPatch_AuditEmitsWithRedaction` — redaction
  invariant test pattern. Mirror.
- `pkg/gateway/public_auth_test.go:131` — table-driven test
  over modes. Extend with `ip_allowlist` rows.
- `cmd/e2e/public_auth_e2e_test.go` — E2E precedent.

## Verification

### Unit (`make test`)

- `migrations/00308_apps_public_auth_ip_allowlist_test.go` —
  `!no_pg` build tag. Apply → seed → RoundTripMixed →
  RejectsSlashZero{V4,V6} → EnumCheckRejectsUnknown.
- `pkg/api/limits_sweep_test.go` extended to call the two new
  accessors across every plan.
- `pkg/api/public_auth_constants_test.go` extended with the
  `ip_allowlist` mode string + the new `MaxEntries` constant.
- `cmd/apid/handlers_public_auth_test.go` extended with 5 new
  tests (plan gate Free/Hobby, plan gate Pro/Scale, size gate,
  `/0` reject, closed-enum-first).
- `pkg/gateway/public_auth_test.go` extended with 3 new rows
  for `ip_allowlist` + 1 misconfig-500 test.
- `pkg/state/app_public_auth_ip_allowlist_test.go` — mirror of
  `app_egress_allowlist_test.go`.

### Lint (`make lint`)

- `golangci-lint v2.4.0` handler-length check — the new gate
  is 30 lines, well under 50.
- `goconst` de-noise — `app.public_auth_ip_allowlist` literal
  appears in the migration + the drift-guard test, well under
  the 3-occurrence threshold.

### Migration slot-fence check

Pre-flight 2026-08-20: PRs #997 (ADR-119 Static outbound IP)
and #991 (deploy-ux Mega-C) fence slots 00304-00307. **00308 is
the next free slot**. Post-merge fence check per
[[ci-stale-slot-fence-after-merge-2026-08-19]].

### Manual E2E

```
gregale app <slug> --public-auth ip_allowlist \
  --ip-allowlist 10.0.0.0/8 \
  --ip-allowlist 2001:db8::/32

gregale app <slug> --public-auth open

curl -H "X-Forwarded-For: 10.0.0.42" https://<surface>/    # 200
curl -H "X-Forwarded-For: 192.0.2.1"  https://<surface>/    # 403
```

### Metal-lima

**Not required.** This PR does not touch `pkg/fcvm`,
`pkg/netns`, or any metal-only path. The runtime gate is
pure-Go in `pkg/gateway/handler.go`.

## Branch

`worktree-feat-public-auth-ip-allowlist` (single PR, single
branch — mirrors `worktree-feat-deploy-stage-progress` pattern).

## Estimated scope

~700-800 LOC across ~14 files (migration + state + apid +
gateway + 4 test files + ADR + drift-guard), one new migration
slot (00308), no new deps, no SDK breakage.
