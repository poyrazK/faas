# ADR-031 · Per-app egress IP allowlist (post-deny accept)

- **Status:** accepted
- **Date:** 2026-07-23
- **Decision:** Add an operator-facing per-app outbound IP allowlist
  on top of the existing global + per-netns denylists. Persist as a
  `cidr[]` column on `apps`; emit as a single v4 accept rule in the
  per-netns forward chain after the lateral-movement deny and SMTP
  drops. Empty allowlist = no rule emitted (current behaviour).
  Per-plan gate: Free/Hobby disallowed; Pro up to 16 entries; Scale
  up to 64 entries. v4 only in v1.

## Context

PR #128 (merged) and PR #151 (merged 2026-07-23) closed tier-1 of
the network roadmap: the host-namespace MASQUERADE, the
boot-persistent `br-tenants` bridge, `ip_forward`, the `ip6 daddr`
deny on the host (ADR-023), and the per-netns `ip6 faas` table.

What remains is **per-tenant egress intent**. The current surface
(`pkg/netns.HostPolicy.ForwardDenyCIDRs` and the per-netns
`NftCommands()` filter) is a single global denylist: every tenant
can reach every public IP not on the denylist. There is no way for
an operator to constrain one specific app's outbound set — the
abuse-desk use case (backscatter from a noisy customer widget)
requires `curl http://only.these.tlds` to actually drop, not to
succeed and then forward to a Sentry capture.

Spec §7 line 348 already enumerates the deny side (SMTP +
RFC1918 + link-local + 100.64/10); the allow side is operator
policy, not a spec contract, so a small ADR captures the rationale
rather than a spec edit.

## Decision

### Storage

Single `cidr[]` column on `apps`, default `'{}'`:

```sql
alter table apps
  add column egress_allowlist cidr[] not null default '{}';

alter table apps
  add constraint apps_egress_allowlist_v4_only
    check (
      cardinality(egress_allowlist) = 0
      or bool_and(family(cidr) = 4)
    );
```

Array column, not a child table. The read pattern is "all CIDRs
for one app on wake" — exactly the apps primary-key lookup; no
join needed. The write pattern is "atomic full-replace" — a
single `UPDATE` vs a `DELETE` + N `INSERT`s in a tx. Migration
00007 precedent (`compute_node` bind fields) made the same call
for small structured data on the apps row.

The CHECK constraint is per-array-element: cidr[] does not carry
a per-element CHECK in Postgres, so the family check is a function
constraint that runs over the array. Empty array stays allowed
(default = "no allowlist"). No second-order index — pk lookup is
the only access path.

### Per-netns emit

A single rule, factored into `forwardAllowlistRule`:

```
nft add rule ip faas forward
  iifname "tap0"
  ip daddr { <egress_allowlist> } accept
```

Position in the chain (per `pkg/netns/config.go::NftCommands`):

```
1. ct state established,related accept
2. ct count over N counter name "faas_cap" drop    [only if ConntrackCap > 0]
3. iifname "tap0" tcp dport { 25,465,587 } drop    ← SMTP deny
4. iifname "tap0" ip daddr { RFC1918... } drop     ← lateral-movement deny
5. iifname "tap0" ip daddr { <allowlist> } accept  ← NEW, only when list non-empty
6. chain policy: accept (empty) | drop (allowlist set)
```

The allowlist sits AFTER every deny → deny wins on overlap. An
operator who typo's `10.0.0.0/8` into the list still gets dropped
by the lateral-movement deny. The chain's POLICY word switches to
`drop` whenever the allowlist is set (`pkg/netns.forwardChainPolicy`),
so un-listed destinations are dropped — pinning this is what closed
PR #159 review F1 (an earlier draft left `; policy accept ;`
unconditional which made the allowlist a no-op on un-listed targets).
Empty allowlist keeps the historical `policy accept` so behaviour is
unchanged for apps that never PATCH.

### Live-instance drift

Live running instances are **not** hot-patched when the operator
changes `apps.egress_allowlist`. The new allowlist is rendered into
the per-netns forward chain at Wake time only. An app whose live
instances were booted with `egress_allowlist = []` continues to
serve traffic under the historical `policy accept` for those
instances even after a PATCH that pins a non-empty list; the new
gate only takes effect on the next wake (cold-boot path) or the
next restore. Matches the precedent set by `RAMMB`,
`MaxConcurrency`, and `ConntrackCap` — all cold-wake-only knobs.

This is deliberate. Hot-patching a running netns would require a
`nft replace rule` against an in-flight chain; the operation is
not idempotent across replays, races with the inbound DNAT, and
opens a window where a packet is matched against a partially-
replaced rule set. A future "live reconfigure" feature is a
separate ADR (rejected below).

### Wire path

The full chain matches the precedent of `pkg/fcvm.Manager.ConntrackCap`:

```
apid (PATCH /v1/apps/{slug})
  → state.UpdateApp(..., EgressAllowlist, SetEgressAllowlist)
  → UPDATE apps SET egress_allowlist = $1
  → pg_notify app_changed (existing trigger)
schedd handleNotification
  → next Wake reads apps.egress_allowlist
  → sched.AppSpec.EgressAllowlist
  → proto vmmd.AppSpec.egress_allowlist
  → pkg/vmmdgrpc.toWakeRequest()
  → pkg/fcvm.WakeRequest.EgressAllowlist
  → pkg/fcvm.Manager.Wake → netns.Config.EgressAllowlist
  → pkg/netns.Config.NftCommands()
```

`app_changed` already covers the wake path (the apid handler
emits on update; schedd loop already logs); no new trigger. Live
running instances keep the OLD allowlist until the next wake —
same semantic as `RAMMB` / `MaxConcurrency` / `ConntrackCap`
today (snapshot captures boot values, live updates only affect
new wakes).

### Per-plan gate

In `pkg/api/limits.go::Limits`, per-plan:

| Plan   | EgressAllowlistAllowed | EgressAllowlistMaxSize |
|--------|------------------------|------------------------|
| Free   | false                  | 0                      |
| Hobby  | false                  | 0                      |
| Pro    | true                   | 16                     |
| Scale  | true                   | 64                     |

apid handler rejects a PATCH on a Free/Hobby app with `403
plan_egress_allowlist_not_allowed` (RFC 7807) and a list longer
than the plan's `MaxSize` with `400 egress_allowlist_too_long`.
The handler validates each entry with `netip.ParsePrefix` before
hitting SQL, so an invalid CIDR is `400 invalid_egress_allowlist`.

### v4 only in v1

The CHECK constraint enforces this. The v6 mirror (separate
`apps.egress_allowlist_v6 cidr[]` or a `family()` variant column)
is deferred to a follow-up ADR; its shape mirrors this one and
the ADR-023 v4/v6 split is the precedent. `forwardAllowlistRule`
is factored to make the v6 mirror a one-line `nft(..., "ip6", ...)`.

## Consequences

- New surface: `apps.egress_allowlist cidr[]` (migration 00029).
- New API field: `UpdateAppRequest.EgressAllowlist *[]string`.
- New plan quotas: `Limits.EgressAllowlistAllowed`,
  `Limits.EgressAllowlistMaxSize` per plan (4 default rows).
- New gRPC field: `AppSpec.egress_allowlist = 7;` (proto slot
  after `sealed_env`).
- New netns emit: `pkg/netns.Config.EgressAllowlist []netip.Prefix`
  + `Config.forwardAllowlistRule`.
- New RFC 7807 codes: `plan_egress_allowlist_not_allowed` (403),
  `egress_allowlist_too_long` (400), `invalid_egress_allowlist` (400).
- Live running instances are NOT hot-patched; the new allowlist
  takes effect on the next wake. Documented in the operator
  runbook.

## Rejected alternatives

- **`pkg/netns/egress_deny.go` (a separate per-app deny table
  join)** — would have expressed the inverse (deny these IPs),
  but the use case is the allowlist (only-these). A "deny
  exceptions to the deny list" double-negative is harder to
  reason about and harder to test.
- **Hot-patching the running netns on every PATCH** — nftables
  supports `nft add rule` / `replace rule` on a live chain, but
  the existing code path treats `netns del` as the only state
  reset for safety reasons (leakcheck invariant §6.2-4/5). A
  live rerender would require a new reconciler goroutine, a
  handle-table cache per netns, and an `nft -c -f` gate on every
  update — disproportionate for v1 and not in scope.
- **v4 + v6 in v1** — the v6 mirror duplicates the same wiring
  with the table-family split already established by ADR-023.
  Shipping v4 only halves the test surface (no FAIL/assert pair
  for `family(cidr) = 6`) and lets the v6 follow-up lean on a
  validated shape. The CHECK constraint deliberately rejects v6
  in v1 — a path-level rejection on insert means a v6-aware
  mirror can be added without a data migration.
- **Per-app IPv6 address-list catalogue (operator-managed
  reusable sets)** — out of scope; not in v1.

## Cross-reference

- `pkg/netns/config.go::forwardAllowlistRule` — emit helper.
- `pkg/netns/config_test.go::TestNftCommandsEmitsAllowlistRule`
  — table-driven coverage.
- `pkg/netns/allowlist_metal_test.go::TestMetalAllowlistRuleInstalled`
  — gated `//go:build metal` install test.
- `pkg/api/limits.go::Limits` — per-plan gate.
- `cmd/apid/handlers_ext.go::updateApp` — RFC 7807 surface.
- `migrations/00029_app_egress_allowlist.sql` — schema + CHECK.
