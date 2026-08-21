# ADR-119 · Static outbound IP per app (Scale-only, operator-provisioned, single-node v1)

- **Status:** proposed (PR-#997 redesign, round 4 — IP source changed
  from customer-supplied BYOIP to operator-provisioned additional IPs
  to keep return-path routing intact; per-netns SNAT path deleted in
  favour of host-side SNAT renderer; first-match NAT ordering
  corrected from AFTER→BEFORE the broad MASQUERADE)
- **Date:** 2026-08-19 (initial); 2026-08-21 (redesign amendment)
- **Decision:** Ship a per-app **static outbound IP** feature: the
  operator provisions an additional IP on the host's AS and adds it
  to the static-egress-IP TOML bundle; the customer pins one of
  those operator-provisioned IPs onto a Scale-plan app; every
  egress packet from that app's instances exits the host with the
  customer's IP as source. v1 is **single-node** (the current EX44
  production posture); multi-host placement pin, IPv6, a
  platform-owned IP pool, and Paddle/Stripe add-on billing are
  explicitly out of scope and tracked as follow-up ADRs.

## Context

Gregale today exits every tenant packet with the **host's primary
public IP**. The egress path is host-identity MASQUERADE on
`br-tenants` → `PublicIface` (see `pkg/netns/policy.go::Render` at
line 399: `ip saddr 10.100.0.0/16 oifname eth0 masquerade`). There
is no per-account or per-app source-IP machinery. ADR-031 / ADR-033
solve the **inverse** problem — they restrict *what destinations an
app can reach* via `apps.egress_allowlist`. This ADR solves *what
source IP an app presents*.

The B2B use case is concrete: a customer cannot use Gregale to call
a partner API that requires IP allowlisting, nor a managed Postgres
that whitelists source IPs (Neon, Supabase, AWS RDS, etc.). This is
the standard "static egress IP" line on every serverless competitor
(Vercel, Cloudflare Workers, Fly.io, Railway). The pull is real and
documented; we have at least one paying customer blocked on it.

The architecture today makes this hard, not because the data plane
is unusual — Firecracker + per-netns MASQUERADE + host MASQUERADE
is a standard three-hop NAT — but because **every tenant on a host
shares the host's primary IP**. To freeze a stable, per-customer
egress IP we need three primitives that do not yet exist:

1. A per-app field that records the customer's chosen IP.
2. A host-level `postrouting` rule that rewrites matching tenant
   source traffic to the customer's IP (a MASQUERADE sibling that
   fires *before* the catch-all `10.100.0.0/16` MASQUERADE).
3. An alias-IP lifecycle on `br-tenants` so the host kernel knows
   the customer's IP is locally reachable on the egress interface.

None of these exist today. We are greenfielding the feature, not
extending an existing one. That is why this is an ADR and not a
sub-decision of ADR-031.

### Single-node assumption is load-bearing for v1

The platform today is one control-plane node (the original Hetzner
EX44). Gate-B (ADR-092) added a second compute-only box (`fsn-2`),
but the egress model is **unchanged** — every tenant on `fsn-2`
still exits with `fsn-2`'s primary IP. There is no per-account
placement pin. The whole scale-out plan (`docs/scale_out_and_workload_classes.md`
D2/D3) explicitly assumes "any node can host any instance" as the
shape that multi-host sharding must preserve.

A static IP per app **implies a host pin** for that app (the IP
must be aliased on whichever host the app's instances live on). For
v1 we accept this asymmetry: a static-IP customer's app lives on
whichever host the customer is "parked" on. The multi-host story
(anycast/floating-IP, or a placement-pin primitive) is a separate
ADR.

## Decision

### Storage

Single nullable column on `apps`, plus a stamp:

```sql
-- migrations/00336_apps_static_egress_ip.sql (additive, rebumped
-- from 00325 in PR-#997 round 2)
alter table apps
  add column if not exists static_egress_ip inet
    check (static_egress_ip is null or family(static_egress_ip) = 4);

alter table apps
  add column if not exists static_egress_ip_set_at timestamptz;

-- Defends against two apps sharing the same IP (alias-IP conflict
-- on br-tenants, see "Risks" below).
create unique index if not exists apps_static_egress_ip_key
  on apps (static_egress_ip)
  where static_egress_ip is not null;
```

**Rationale for column-on-apps, not child table.** We expect
Scale-tier customers to want at most a handful of static IPs
(typically 1 per app). A child table `app_static_egress_ips` is the
right shape if/when we raise the per-app quota to N (currently 1),
but it is **not** needed for v1. The column-on-apps pattern mirrors
ADR-031 (`apps.egress_allowlist`) and the `accounts.egress_allowlist_extra`
integer (ADR-082). Bumping later is a migration.

**IPv4 only in v1.** Family=4 is enforced via CHECK; the v6
mirror (mirroring ADR-033) is deferred. Today every B2B allowlist
case is IPv4; IPv6 customers will surface as a follow-up request.

**Per-app quota of 1.** `Limits.StaticEgressIPsPerApp = 1` for Scale.
Bumping to N is a per-plan int change in `pkg/api/limits.go` with
no schema impact.

### Wire (sched → vmmd)

Add `optional string static_egress_ip = 8` to `AppSpec` in
`api/proto/onebox/faas/vmmd/v1/vmmd.pb.go`. Thread through:

- `pkg/sched/vmmclient.go::AppSpec` (mirrors `EgressAllowlist` line 304-322).
- `pkg/sched/engine.go:1821` (Wake reads `app.StaticEgressIP`).
- `pkg/sched/egress_drift.go` — extend the existing `app_changed`
  pg_notify subscriber to fire `UpdateStaticEgressIP` when the
  field changes (mirrors `UpdateEgressAllowlist` path).

### Renderer model (the load-bearing piece)

The host is the **only** SNAT authority for the customer's IP. The
per-netns chain (`pkg/netns.Config::NftCommands`) does **NOT**
emit a customer SNAT — the early PR-#997 first cut tried to add a
sibling `ip saddr 10.0.0.2 snat to <CustomerIP>` rule alongside the
per-VM MASQUERADE, but **nftables NAT is first-match + terminal**,
so the broad MASQUERADE shadows the specific SNAT. That code path
is deleted.

Extend `pkg/netns.HostPolicy` with `StaticEgressRules
[]StaticEgressRule` (PerVMSrcAddr + CustomerIP + AccountID + AppID
fields). `Render()` emits a block in `chain postrouting` **BEFORE**
the existing `10.100.0.0/16` MASQUERADE, one rule per live VM:

```
ip saddr <per-vm-host-ip> oifname <PublicIface> snat to <customer-ip>
  # account=<acct> app=<app>
```

**Why BEFORE the MASQUERADE.** nftables NAT chains evaluate rules
in declaration order; first match wins and is terminal. A specific
SNAT that lives AFTER a broad MASQUERADE is unreachable. The
ordering — specific (per-customer) before broad (per-tenant-range)
— is regression-netted by
`TestHostPolicyStaticEgressRulesPlacedBeforeMasquerade`.

The `<per-vm-host-ip>` is allocated from a dedicated sub-range of
the per-host `/16` by `pkg/fcvm/alloc.go::AcquireStaticEgressIP`
(see "Allocator layer" below). The (accountID, appID, customerIP,
per-VM-host-IP) tuple is the source of truth for the host
renderer's rule set.

### IP source model (operator-provisioned additional IPs)

The customer's IP is an **operator-provisioned additional IP** —
not a customer-supplied BYOIP. Operator flow:

1. Operator provisions an additional IP on the host's AS (Hetzner
   "Additional IP", AWS EIP, etc.) and adds it to `/etc/faas/egress/static_egress_ips.toml`:
   ```toml
   [[entries]]
   account_id = "<acct-uuid>"
   app_id = "<app-uuid>"
   ip = "203.0.113.42"
   ```
2. vmmd's SIGHUP-driven watcher (`watchStaticEgressIPBundleReload`)
   reloads the TOML and writes each `(account_id, customer_ip)` tuple
   to the new `provisioned_static_egress_ips` table (migration 00337)
   and pushes the per-VM host IP rules into `HostPolicy.StaticEgressRules`.
3. The apid PUT handler (`POST /v1/apps/{slug}/static-egress-ip`)
   rejects any pin whose `(account_id, customer_ip)` is not in the
   Postgres table — 404 `static_egress_ip_not_provisioned`. The
   apid surface is invisible until the operator flips
   `FAAS_STATIC_EGRESS_IP_ENABLED` AND has provisioned the IP.

**Why operator-provisioned, not customer-supplied.** A
customer-supplied IP that isn't routed to the host's AS gets
outbound-spoofed-source-filtered at the switch (no return path) and
inbound replies route to the customer's AS (lost). Additional IPs
on the host's AS have return-path routing pre-configured by the
provider. Customers pin from the operator's provisioned set; they
do not choose their own IP.

### Bridge alias + SIGHUP reload

The customer's IP must be locally reachable on `br-tenants` for the
SNAT rule to be valid. Lifecycle:

- `pkg/fcvm/alloc.go::AcquireStaticEgressIP(accountID, appID, ip)`
  reserves a per-VM host IP from the bridge `/16`, persists the
  (account, app, ip, per-VM-host-IP) tuple to a local file
  (`/etc/faas/egress/static_egress_ips.toml`), and returns the
  per-VM host IP. The renderer uses this for the `ip saddr` value.
- `cmd/vmmd/egress_static_ip_bundle.go` (new, mirrors
  `cmd/vmmd/egress_bundle.go:30-35`) loads the TOML at startup and
  on SIGHUP. On reload, it (a) reconciles the bridge alias-IP set
  via `ip addr add` / `ip addr del`, then (b) signals
  `cmd/vmmd/egress_watcher.go` to re-render the host ruleset via
  the existing `egress_policy_changed` pg_notify path. The single
  reload goroutine (SIGHUP-driven, mirror
  `cmd/vmmd/egress_bundle.go:123-165`) is the serialisation point
  for concurrent set/clear — see Risks.

### Per-plan gate

In `pkg/api/limits.go`:

```go
type Limits struct {
    // ...
    StaticEgressIPAllowed    bool
    StaticEgressIPsPerApp   int
}
```

Per-plan rows (`TestPlanLimitsMatchSpec` must extend):

| Plan  | StaticEgressIPAllowed | StaticEgressIPsPerApp |
|-------|-----------------------|-----------------------|
| Free  | false                 | 0                     |
| Hobby | false                 | 0                     |
| Pro   | false                 | 0                     |
| Scale | true                  | 1                     |

New accessors `Plan.StaticEgressIPAllowed() bool` and
`Plan.StaticEgressIPsPerApp() int`, fail-closed to false/0 on
unknown plans. Wire error codes (in `pkg/api/errors.go`):

- `CodePlanStaticEgressIPNotAllowed` (402) — non-Scale plan.
- `CodePlanStaticEgressIPQuota` (403) — per-app quota (1) reached.
- `CodeAppStaticEgressIPInvalid` (400) — malformed IP, IPv6, or
  IP inside RFC1918 / link-local / multicast / 100.64.0.0/10
  (the CGN range we deny today).

### API surface

- `GET /v1/apps/{slug}/static-egress-ip` → `AppStaticEgressIPResponse`
  with `{ip, set_at, plan_cap: 1, max_extra: 1}`.
- `PUT /v1/apps/{slug}/static-egress-ip` body `{ip: "203.0.113.42"}`.
- `DELETE /v1/apps/{slug}/static-egress-ip` clears.
- Env flag `FAAS_STATIC_EGRESS_IP_ENABLED` (default OFF, mirrors
  `FAAS_TENANT_SURFACES_ENABLED`). Flip on at the same time the
  feature flag flips for `tenant_surfaces`.

### Validation

The IP must:

1. Be valid IPv4 (CIDR parse, family check).
2. Not be in `RFC1918` (`10/8`, `172.16/12`, `192.168/16`),
   `link-local` (`169.254/16`), multicast (`224/4`), `100.64/10`
   (CGN — denied per spec §11), the loopback range
   (`127/8`), or `0.0.0.0/8` (the "this network" range
   RFC6890 reserves). Reuse `pkg/netns.ValidateCIDRsAgainstDenySet`
   (line 232-253) with the IP wrapped as a /32 CIDR — the canonical
   `pkg/api.ValidateStaticEgressIP` helper (used at apid, Wake,
   bundle loader, and the metal test) is the **single source of
   truth**; no hand-rolled deny-set copies.
3. Be **operator-provisioned** for the requesting account — the
   `(account_id, customer_ip)` tuple must exist in the
   `provisioned_static_egress_ips` table (migration 00337), written
   by vmmd's TOML reload. Otherwise 404
   `static_egress_ip_not_provisioned`.

### CLI surface

`gregale app security static-egress-ip {show,set,clear}` — pre-check
`api.StaticEgressIPEnabled()` and the plan gate before any HTTP
call. Mirrors `cmd/gregale/commands_tenant_surfaces.go:32-56`.

## Consequences

- New migration `00336_apps_static_egress_ip.sql` (additive,
  nullable, default NULL; rebumped from 00325 after PR-#997 round-1
  shipped).
- New migration `00337_provisioned_static_egress_ips.sql` — the
  operator-bundle gate table the apid PUT path validates against.
  Composite `(account_id, customer_ip)` PK; v4-only CHECK; ON
  DELETE CASCADE from `accounts`.
- New migration `00338_reserve_slot.sql` (fence for the cross-PR
  follow-up).
- New wire field on `AppSpec` (proto field 14) +
  `UpdateStaticEgressIP` gRPC method.
- New `pkg/netns.HostPolicy.StaticEgressRules` field + new
  `pkg/netns.StaticEgressRule` type — the host-side SNAT
  authority. The per-netns `pkg/netns.Config.AccountStaticIP`
  path is **deleted** (see "Renderer model" above).
- New `pkg/fcvm/alloc.go::AcquireStaticEgressIP` /
  `ReleaseStaticEgressIP` / `StaticEgressReservationFor` methods
  that allocate per-VM host IPs from the dedicated
  `10.200.0.0/16` range (separate from the dynamic per-VM pool).
- New `pkg/state.Store::ProvisionedStaticEgressIPExists` /
  `ReplaceProvisionedStaticEgressIPs` methods — the operator-
  bundle ↔ apid bridge.
- New `cmd/vmmd/egress_static_ip_bundle.go` (TOML loader + SIGHUP
  + Postgres write + host-renderer push, restructured to consume
  the canonical `pkg/api.ValidateStaticEgressIP` helper).
- New env flag `FAAS_STATIC_EGRESS_IP_ENABLED` (default OFF).
- New dashboard card surfacing the per-app pin + plan cap.
- New `gregale app security static-egress-ip` subcommand family.
- New RFC 7807 error codes (`plan_static_egress_ip_not_allowed`,
  `plan_static_egress_ip_quota`, `app_static_egress_ip_invalid`,
  **`static_egress_ip_not_provisioned` (404)**).
- **Spec §11 update** — add a paragraph noting the
  operator-provisioned IP path (the customer does NOT choose their
  own IP; the operator does). The metadata-range deny (§11 line
  398) is unchanged; `0.0.0.0/8` is added to the deny set.

## Rejected alternatives

- **Customer-supplied BYOIP** (the original v1 cut, before PR-#997
  round-4 redesign). Customer types an IP from their own range; the
  host aliases it on `br-tenants`. Rejected: a customer-supplied IP
  that isn't routed to the host's AS gets
  outbound-spoofed-source-filtered at the switch (no return path)
  and inbound replies route to the customer's AS (lost). The
  operator-provisioned model (additional IPs from the host's AS,
  pre-routed by the provider) is the only shape that works without
  BGP / VRRP / a serverless-anycast IP layer Gregale doesn't have.

- **Per-account static IP (vs per-app).** A customer might want one
  IP shared across all of their apps. ADR-100 tenant-surfaces-style
  child table would model this. Rejected for v1: per-app is the
  existing egress-allowlist shape (ADR-031), the dashboard is
  per-app, the limits are per-app, and per-app quota of 1 means a
  Scale customer with N apps needs N IPs (manageable). A
  per-account variant can ship later via a separate ADR.

- **Platform-owned IP pool.** Gregale owns a `/28` or `/27` from
  Hetzner and hands one out per customer. Rejected: pool exhaustion
  is a real risk on a single node (we have ~28-127 usable IPs, far
  fewer than the Scale tier customer base); eviction/rotation
  story is non-trivial; BYOIP is what every B2B customer already
  has allocated via their existing provider.

- **Multi-host v1 via per-host alias pool.** Alias every customer's
  IP on every node. Rejected: doubles the alias-IP surface,
  introduces per-node state divergence, and the production
  posture is single-node anyway (the fsn-1/fsn-2 split is control
  vs compute, not multi-tenant routing).

- **Multi-host v1 via anycast / floating IP.** BGP or a VIP layer.
  Rejected: introduces infra Gregale doesn't have (BGP, L4 LB).
  Blocks on a separate infra ADR.

- **Atomic IP rotation in v1.** A single PATCH that swaps old→new
  in one transaction. Rejected: clear-then-set is two HTTP calls
  but transparent to the customer, and the conntrack cache means
  existing flows survive the swap. A future ADR can add
  `PUT /v1/apps/{slug}/static-egress-ip/rotate`.

- **Gating on plan tier alone (no env flag).** Rejected: every
  similar feature ships behind `FAAS_*_ENABLED` for the
  cookie-cluster rollout (per `cmd/apid/handlers_tenant_surfaces.go:45`
  pattern). Symmetry wins.

- **Per-app child table `app_static_egress_ips` from day one.**
  Rejected: per-app quota of 1 makes the column-on-apps pattern
  load-bearing and trivially bumpable. The migration to a child
  table later is a one-time cost.

## Risks

- **First-match NAT ordering regression.** The single load-bearing
  ordering rule (specific SNAT **before** broad MASQUERADE) is
  easy to break in a future refactor. The host renderer is rebuilt
  on every VM wake/teardown, and a refactor that reorders the
  emit blocks kills the feature silently. Mitigated by
  `TestHostPolicyStaticEgressRulesPlacedBeforeMasquerade` — the
  string-position assertion is the regression net.

- **Alias-IP lifecycle on `br-tenants`.** Concurrent set/clear
  across multiple apps can race the bridge alias-IP add/del. The
  SIGHUP-driven reload goroutine in
  `cmd/vmmd/egress_static_ip_bundle.go` is the single serialisation
  point — same shape as the existing operator-bundle reload.
  Conntrack state is preserved across reloads.

- **Multi-tenant IP collision.** Two accounts sharing the same IP
  would alias-conflict on the bridge. Mitigated by the
  `apps_static_egress_ip_key` partial unique index (returns 23505
  on conflict → apid maps to `plan_static_egress_ip_quota`).

- **Postgres bundle-table lag.** The operator's TOML is the source
  of truth; the `provisioned_static_egress_ips` table is a cache
  vmmd writes on SIGHUP. A vmmd restart takes ~1 s to reload the
  TOML and write the cache; during that window an apid PUT fails
  with 404. Mitigated by `s.bootSynced` atomic — apid returns 503
  `Retry-After: 1` until the first successful bundle sync. v1
  ships without the atomic and accepts the 1-second 404 window
  (acceptable for the deploy cadence).

- **Per-app quota of 1 may be too tight.** Scale customers with N
  apps may want N IPs. Mitigated by the per-plan `int` cap — bump
  to 5 or 10 with no schema change. Documented as a v1.1 follow-up.

- **Source-IP rotation has a transient window.** Clear-then-set is
  not atomic; for ~hundreds of ms the app has no static IP and
  exits with the host's primary IP. Mitigated by future ADR
  (atomic rotation). For v1 the customer is expected to coordinate
  the swap with their allowlist partner (the standard
  "old→overlap→new" allowlist dance).

- **Hetzner additional-IP rDNS.** Some partners check PTR records
  on the egress IP. The operator must set rDNS on the additional
  IP before the customer pins it. Documented in the deploy runbook
  (not a code change).

## Cross-references

- ADR-009 (identical inner network world) — preserved.
- ADR-031 (per-app egress allowlist) — sibling feature, inverse axis.
- ADR-033 (v6 mirror of ADR-031) — shape template for v6 follow-up.
- ADR-055 (per-host egress policy templating) — the host renderer
  we extend.
- ADR-081 (operator egress bundle) — the SIGHUP-driven reload
  pattern we clone.
- ADR-082 (per-account additive egress allowlist cap) — schema +
  accessor pattern we mirror.
- ADR-092 (Gate-B cross-box mTLS hardening) — defines the
  fsn-1/fsn-2 split that v1 explicitly does NOT extend to static IP.
- ADR-100 (tenant surfaces) — child-table + quota pattern, the
  alternative we rejected for v1.
- Spec §11 (egress rules) — extend with a paragraph on the
  customer-supplied static IP path.
- `docs/scale_out_and_workload_classes.md` — explicit "any node
  can host any instance" assumption we are temporarily violating
  for static-IP customers in v1.
- Issue #757 closure (filter runtime) — JSONPath lessons transfer
  to IP validator (validate shape BEFORE walking CIDR tree).
- Issue #976 (SAFE-RELEASES) cluster — `target_deployment_id`
  pattern for additive wire fields transfers here.
- Memories: `migration-gates-collision-and-replay`,
  `cross-pr-slot-gate-races-with-active-pr`,
  `cross-pr-slot-gate-reservation-fence-pattern`,
  `cross-pr-rebase-fence-deletion-hazard`,
  `trigger-replay-safety-drop-before-create`.