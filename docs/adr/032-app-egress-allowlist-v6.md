# ADR-032 · Per-app egress IP allowlist, IPv6 mirror

- **Status:** accepted
- **Date:** 2026-07-24
- **Decision:** Extend ADR-031's per-app egress allowlist to v6 by
  swapping the DB CHECK for a "v4-or-v6, non-`/0`" guard on the
  existing single `apps.egress_allowlist cidr[]` column and
  partitioning the rendered nft rules by family at the renderer.
  Wire shape, store shape, and `netns.Config.EgressAllowlist` stay
  single; the per-family table split (ADR-023) is already in place,
  so the v6 chain only needs the same allowlist `accept` step that
  was added to the v4 chain in ADR-031.

## Context

ADR-031 explicitly deferred the v6 mirror as a separate ADR:

> The v6 mirror (separate `apps.egress_allowlist_v6 cidr[]` or a
> `family()` variant column) is deferred to a follow-up ADR; its
> shape mirrors this one and the ADR-023 v4/v6 split is the
> precedent. `forwardAllowlistRule` is factored to make the v6 mirror
> a one-line `nft(..., "ip6", ...)`.

ADR-023 (merged earlier in tier-1) already established the
per-family table split: the per-netns forward chain is mirrored on
a separate `ip6 faas` table + chain because nft rejects mixing `ip`
and `ip6` matches in one table. The v6 chain already carries `ct
state established,related accept`, the per-instance connlimit cap
(via `forwardConnlimitRule6`), and the v6 lateral-movement deny —
it lacks only the allowlist `accept` step.

The gap today: a tenant can pin an IPv4 allowlist, but IPv6 traffic
bypasses it entirely (the v6 chain-policy accept + the global v6
deny set are the only constraints). For a Pro+ tenant trying to
constrain an app to a known set of upstream services, that
asymmetry is a paper-cut before the next bigger item (live-instance
drift, deny-line audit).

## Decision

### Storage — trigger contract swap on a single column

The v4-only `apps_egress_allowlist_v4_only` CHECK constraint from
migration 00029 is dropped and replaced by a BEFORE-row PL/pgSQL
trigger (`apps_egress_allowlist_cidr`, migration 00030) that
enforces:

1. Each entry's `family(c)` is 4 or 6 (no exotic families).
2. Each entry's `masklen(c)` is non-zero.

The non-`/0` rule is the same one apid and vmmd already enforce on
the wire — the DB trigger is the source of truth; the apid + vmmd
layers are defence-in-depth (per ADR-031 review F3's wire-bypass
concern).

The column stays single. Existing rows are 100% v4 by construction
(the v4-only trigger rejected every v6 write since 00029 landed),
so this is a contract swap, not a data migration. Down-migrate
restores the v4-only trigger verbatim so 00029's `RejectsV6`
regression net still passes on a clean reverse.

### Renderer — partition v4/v6 internally

`pkg/netns/config.go::forwardAllowlistRule` (v4-only helper) and
`forwardAllowlistRule6` (v6-only sibling, new at 032) together
cover the per-family partition. Both return a single `[]string`
argv (or nil when their half is empty) — the split is into two
helpers, not a single `[][]string`. Internally each helper
partitions the single `EgressAllowlist` slice via
`prefix.Addr().Is4()` and emits one argv:

```
nft add rule ip  faas forward iifname "tap0" ip  daddr { <v4 list> } accept   [v4 only]
nft add rule ip6 faas forward iifname "tap0" ip6 daddr { <v6 list> } accept   [v6 only]
```

`NftCommands()` calls each helper at its own position in its
chain's command stream — the v4 helper is invoked after the v4
lateral-movement deny (existing position), the v6 helper after the
v6 lateral-movement deny (new placement inside the v6 chain block).

The comma-joined set inside `{ … }` with no trailing whitespace is
preserved (modern-nft syntax; PR #128's regression net;
`nft-cidr-set-comma-required` memory).

### Wire / store / limits — unchanged

- `api/proto/.../vmmd.proto::AppSpec.egress_allowlist = 7` — still
  one `repeated string`. Comment updated to mention v6.
- `pkg/sched/vmmclient.go::AppSpec.EgressAllowlist` — still one
  `[]string`. Comment updated.
- `pkg/state.App.EgressAllowlist` — still one `[]netip.Prefix`.
- `pkg/api/limits.go::EgressAllowlistMaxSize` — still one per-plan
  int (Pro 16, Scale 64) shared by v4 + v6 combined.

No wire-shape version bump; v6 is a superset capability on the
same field. apid's `validateUpdateApp` and `pkg/fcvm.Manager.Wake`
drop their `!prefix.Addr().Is4()` reject (kept `Bits() == 0`).

### Plan gate

Unchanged. Free/Hobby always empty (403
`plan_egress_allowlist_not_allowed`); Pro up to 16 combined
entries; Scale up to 64 combined entries. No per-family split —
the budget is the same regardless of mix.

### Chain policy — flips on the single field, both chains

`forwardChainPolicy` (config.go) checks `len(c.EgressAllowlist) == 0`
only — no per-family split. So a v6-only or mixed allowlist flips
**both** chains to `policy drop`, even if the v4 chain has no
allowlist rule of its own. This is deliberate: an operator pinning
v6 destinations has signalled intent to constrain egress, so v4
falls back to default deny rather than the historical default-accept.
A future per-family policy toggle (e.g. "v4 default-accept, v6
allowlist-only") is a separate ADR if a customer needs the
asymmetry.

## Consequences

- New migration: `00030_app_egress_allowlist_v6.sql` (drops the
  v4-only trigger + function; creates `apps_egress_allowlist_cidr`
  trigger + function). Slot collision risk (PR #153 + PR #159 each
  renumbered once); if a parallel PR claims 00030 first, renumber
  per the existing precedent.
- apid + vmmd wire layers accept v6 entries (defence-in-depth
  gates loosened; DB trigger is the new source of truth).
- Renderer partition is the only behaviour change for egress: two
  nft argvs emitted instead of one when the input mixes families;
  one argv emitted (either v4 or v6) when the input is single-family.
- Empty allowlist semantics unchanged: no rule emitted, chain
  policy stays `accept`.
- The `apps_egress_allowlist_v4_only` constraint name disappears.
  Operators referencing it in dashboards / alerts must update to
  `apps_egress_allowlist_cidr`.

## Rejected alternatives

- **Two columns: `egress_allowlist_v4 cidr[]` + `egress_allowlist_v6
  cidr[]`** — Approach C in the design discussion. Doubles the
  surface (two proto fields, two store fields, two Config fields,
  two plan caps or a single combined cap). The family split is an
  internal rendering detail — operators see one allowlist. Rejected
  for symmetry cost.
- **One column, CHECK constraint with `bool_and(family() IN (4,6))`**
  — the natural `CHECK` form. Was actually attempted in 00029 and
  failed at runtime: Postgres rejects CHECK constraints that
  reference a column inside a subquery/`bool_and`, the constraint
  body gets compiled to a boolean expression over the column but
  the per-array-element guarantee is not enforced. The
  BEFORE-row trigger is the only Postgres-blessed way to enforce
  per-element constraints on an array column. Rejected on first
  principles + 00029 empirical failure.
- **Per-family cap (split `EgressAllowlistMaxSize` into `v4Max +
  v6Max`)** — would protect a future ops request from a tenant
  filling the budget with one family and starving the other. Not a
  current concern (no plan ships an asymmetry); can be added later
  without a contract break (a single cap → per-family caps is a
  weakening, not a tightening). Deferred.
- **`::ffff:1.2.3.0/120` (v4-mapped IPv6) canonicalisation** — an
  operator typing `1.2.3.0/24` and `::ffff:1.2.3.0/120` should
  produce the same effective set. Out of scope for v1; both
  formats survive the trigger (both are valid v6 CIDRs), the
  renderer emits both as-is, and the duplicate-match overlap is
  benign (the nft semantics are union). A future "normalise at
  write" is one-line in `validateUpdateApp` if it becomes a
  customer complaint.
- **Read-path exposure on `api.AppResponse`** — the API today only
  accepts `egress_allowlist` via PATCH; GET does not surface it.
  Same status as ADR-031; not in scope for this slice either.
- **Deny-line audit (issue #146)** — adjacent hygiene PR that
  deserves its own ADR once this slice merges.

## Cross-reference

- `migrations/00030_app_egress_allowlist_v6.sql` — trigger swap.
- `migrations/00030_app_egress_allowlist_v6_test.go` — DB-level
  tests (RoundTripV6, RoundTripMixed, RejectsSlashZeroV4/V6).
- `migrations/00029_app_egress_allowlist_test.go` —
  `RejectsV6` → `AcceptsV6` rename on clean-reverse.
- `pkg/netns/config.go::forwardAllowlistRule` — emit helper,
  widened to `[][]string`.
- `pkg/netns/config_test.go` — extended coverage (TestForwardAllowlistRuleHelper,
  TestNftCommandsEmitsAllowlistRule, etc.).
- `pkg/netns/allowlist_metal_test.go::TestMetalAllowlistV6RuleInstalled`
  — gated `//go:build metal` runtime install test.
- `cmd/apid/handlers_ext.go::validateUpdateApp` — dropped
  `!Is4()` reject.
- `cmd/apid/handlers_ext_test.go::TestUpdateAppEgressAllowlist_V6AcceptedOnPro`
  (+ _MixedAcceptedOnPro, _SlashZeroRejectedV6) — handler-level
  acceptance + per-family gate.
- `pkg/fcvm/manager.go::Wake` — dropped `!Is4()` reject; updated
  error message wording to drop "v4 only".
- `pkg/state/pgstore_v6_allowlist_test.go::TestPgStore_UpdateApp_*`
  — pgStore round-trip + SQLSTATE 23514 pin.
- `pkg/api/errors.go::ErrInvalidEgressAllowlist` — message
  wording: "v4 or v6 CIDR (non-/0)".
- ADR-031 — parent ADR (the v4-only v1 this slice extends).
- ADR-023 — per-family table split precedent this slice leans on.