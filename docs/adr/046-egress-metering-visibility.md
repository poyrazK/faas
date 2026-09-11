# ADR-046 — Per-instance egress metering and gated billing

- **Status:** Accepted
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-07-29
- **Amended:** 2026-09-11. Customer totals use `net_tx_bytes` alone (gateway
  `tx_bytes` is a diagnostic subset); meterd checkpoints vmmd's cumulative
  counters transactionally; failed gateway stream sends are requeued; and
  provider delivery receipts are meter-qualified. Charging remains disabled
  until a provider explicitly advertises the egress capability and product
  pricing/included quota are approved.
- **Amended:** 2026-09-12. Polar can deliver net calendar-month egress
  overage through a separate meter. The feature defaults off; shadow sends no
  provider events; live requires explicit per-plan allowances, price, meter,
  event, and non-retroactive activation hour. The existing customer overage
  cap includes live egress, and the policy is visible in API/CLI/dashboard.
- **Decision:** Land a per-instance customer egress byte counter (vmmd tc/veth
  scrape + gateway response byte counter) end-to-end into `usage_minutes` and
  `GET /v1/usage` / `/v1/usage/summary` / `/v1/account/export` / `faas usage`,
  without touching the billing surface. The data is sampled, persisted, and
  exposed — but **no provider push is added** in this set of PRs. The
  `Provider.PushUsageRecord` extension (Stripe / Paddle) is the follow-up that
  lands on top of this seam.
- **Why:** §7 already enforces egress via per-plan `tc tbf` caps and nftables
  deny rules, and §10 explicitly leaves egress unbilled (1 Gbit flat), but no
  per-instance byte measurement exists. ADR-039 established the
  measurement-only precedent for CPU; the platform now needs the same seam for
  egress so a future billing PR has data to bill on. The owner has decided to
  add egress to billing **later** but needs the metering seam ready **now**.

## Context

Spec §10 declares "Egress not billed (1 Gbit flat)" (`docs/faas_implementation_spec.md:445`)
and §7 enforces egress via the per-plan `tc tbf` cap on the root-side `vethHost`
(10 / 25 / 100 / 250 Mbit per plan). Page A.10 says "scoped: producer meter; wire
audit: enable-rate cutover" — the meter is the seam to lift for billing.

The existing `cpu_usec` visibility (ADR-039, PR #346, migration 00055) is the
template. It added a `usage_minutes.cpu_usec` column with additive `ON CONFLICT`
merge semantics, recreated the `usage_monthly` view to `SUM(cpu_usec)`, exposed
`cpu_usec` / `used_cpu_hours` through every public usage surface, and **did not
push to a billing provider**. ADR-046 mirrors this pattern for egress bytes.

There are two distinct egress data sources that the platform can sample:

1. **HTTP response bytes the gateway wrote** to the client. Available at
   `pkg/gateway/forwardproxy.go:222` (response body length) and at the
   `statusRecorder` wrapper in `pkg/gateway/handler.go:572-595`. Exact count of
   what the client received. Misses egress that does not pass through the
   gateway response writer (DNS-over-UDP responses, websocket frames the
   gateway doesn't see, future non-HTTP surfaces).
2. **Per-instance netns tap0 egress bytes** — the cumulative byte counter on
   the root-side `vethHost` interface. Available at
   `/sys/class/net/<vethHost>/statistics/rx_bytes`. Catches every byte the
   guest transmits on tap0 (HTTP + DNS + websockets + outbound TCP keepalives),
   regardless of which surface carries it. Includes Ethernet framing so the
   count is "interface bytes" not "IP bytes" or "payload bytes".

Both are useful. The future billing PR picks the unit; this set of PRs freezes
the **interface bytes on root-side `vethHost.rx_bytes`** as the canonical
source (`net_tx_bytes`) and persists the gateway-side count (`tx_bytes`) as a
parallel, exact-looking mirror for incident/audit correlation.

The meterd single-writer invariant on `usage_minutes` (CLAUDE.md ownership
table) is preserved: both producers accumulate into in-process ring buffers;
the `meterd.Sampler.SampleAndRoll` is the only path that calls `AppendUsage`.

## Decision

### 1. Product unit and counter source

The interface-byte counter on root-side `vethHost.rx_bytes` is the canonical
source. The veth topology (`pkg/netns/config.go:28-43`) is

```
root ns:    br-tenants ── vethHost
netns:      vethPeer ── tap0 ── guest
```

Customer egress traverses `tap0 → vethPeer → vethHost`. On root-side
`vethHost` this is **RX**. Reading `vethHost.tx_bytes` would count
gateway → guest (ingress), not customer egress. The kernel-counter file is
`/sys/class/net/<vethHost>/statistics/rx_bytes` and it is the same counter the
`tc tbf` qdisc reads, so the cap and the meter are consistent.

Pinned by a `//go:build metal` test that boots a real guest, generates scripted
egress, and asserts the kernel `rx_bytes` delta equals the persisted
`net_tx_bytes` sum within 0 bytes. The ADR is invalid if that test fails.

`vmmd_egress_deny_total` (deny-telemetry at §12.2) is **not** the byte source
— it counts denied packets, not transmitted bytes.

### 2. Two columns: `tx_bytes` and `net_tx_bytes`

`usage_minutes` gains two additive-merge columns:

| Column | Source | Counts | Unit |
|---|---|---|---|
| `tx_bytes` | gateway `statusRecorder` | HTTP response body bytes to the client | exact payload bytes |
| `net_tx_bytes` | vmmd `netstats.Cache` | root-side `vethHost.rx_bytes` delta | interface bytes (incl. framing) |

Both are cumulative per-minute deltas, additive on `(instance_id, minute)`
(because the meterd sampler can call `AppendUsage` many times per minute; the
billing-floor `mb_seconds` and `requests` columns keep first-write-wins —
ADR-039 §2 asymmetry). The schema change is at
`migrations/00065_usage_minutes_egress.sql`.

The future billing PR chooses the unit. This set of PRs freezes both as
distinct persisted fields so the next PR can pick, average, or `max()` without
re-migrating.

### 3. vmmd → schedd → meterd wire path

```
vmmd netstats.Cache (cumulative rx_bytes per instance, regression-safe)
  └─ cmd/vmmd network_poller (250ms loop, reads /sys/class/net/<vethHost>/statistics/rx_bytes)
      └─ pkg/vmmdgrpc.Stats RPC (additive tx_bytes field, end of message)
          └─ schedd instancestats.Poller (per-instance accumulator)
              └─ pkg/scheddgrpc.ListInstanceStats RPC (additive tx_bytes field)
                  └─ meterd scheddEgressAdapter (mirror of scheddCPUAdapter)
                  └─ pkg/meter.Sampler.SampleAndRoll
                          └─ pkg/state.AppendNetworkUsageObservation
                              (checkpoint + usage delta in one transaction)
                              └─ usage_monthly view (SUM(tx_bytes), SUM(net_tx_bytes))
                                  └─ apid /v1/usage, /v1/usage/summary, /v1/account/export
                                      └─ faas usage (informational panel)
```

The gateway-side path is symmetric:

```
gateway statusRecorder.Bytes (per HTTP request)
  └─ cmd/gatewayd egressSink (per-(instance, minute) ring buffer)
      └─ pkg/gateway/egressgrpc.StreamBytes (gRPC stream over /run/faas/gatewayd.sock)
          └─ meterd gatewayEgressAdapter
              └─ pkg/meter.Sampler.SampleAndRoll
                  └─ pkg/state.AppendUsage(tx_bytes, ...)
```

Both producers are additive on the same `(instance_id, minute)` row. The
internal Postgres INSERT key is unchanged; the new persistence is purely
additive.

### 4. Persistence

`migrations/00065_usage_minutes_egress.sql` adds:

```sql
alter table usage_minutes
    add column if not exists tx_bytes bigint not null default 0,
    add column if not exists net_tx_bytes bigint not null default 0;
```

and recreates `usage_monthly` to `SUM(tx_bytes)` and `SUM(net_tx_bytes)`. The
`AppendUsage` SQL inherits the additive-merge shape from `cpu_usec`:

```sql
on conflict (instance_id, minute) do update
   set cpu_usec     = usage_minutes.cpu_usec + EXCLUDED.cpu_usec,
       tx_bytes     = usage_minutes.tx_bytes + EXCLUDED.tx_bytes,
       net_tx_bytes = usage_minutes.net_tx_bytes + EXCLUDED.net_tx_bytes
```

The `mb_seconds` and `requests` columns keep first-write-wins. Existing
AppendUsage callers (PK-1, M7 hardening) are not affected.

### 5. Public usage surfaces

`GET /v1/usage`, `/v1/usage/summary`, `/v1/account/export`, and the `faas usage`
CLI all expose `tx_bytes` and `net_tx_bytes` (and `used_egress_gb` for parity
with `used_cpu_hours`). `used_egress_gb` is derived from `net_tx_bytes` only;
adding the gateway payload counter would double-count HTTP responses. OpenAPI
mirrors via `make spec-sync`. The informational disclaimer mirrors `cpu_usec`.

`pkg/appmetrics` gains a `gateway_response_bytes_total{app,plan}` rollup so
the dashboard surfaces per-app egress rate (bytes/sec). No per-instance
Prometheus label — the byte data lives in `usage_minutes`, queryable per
account.

### 6. Billing boundary

No Stripe / Paddle / Polar egress push. No pricing constant change. No plan
limit change. No financial-model edit. The provider abstraction exposes an
optional multi-meter interface and capability, but no current provider
advertises it. A separate meter ledger uses the durable delivery key
`(provider, account_id, meter, window_start)` while the existing compute ledger
stays unchanged for rolling compatibility. An egress receipt therefore cannot
suppress compute delivery for the same hour.

The seam is `usage_minutes.tx_bytes` and `usage_minutes.net_tx_bytes`. The
follow-up PR adds `Provider.PushUsageRecord` (or a sibling) and a parallel
pusher; both are out of scope for ADR-046.

### 7. Observability and acceptance

- `vmmd_egress_source_errors_total{node}` (pre-instantiated) — counter-read
  failure on the source interface.
- `schedd_egress_partial_errors_total{node}` (pre-instantiated) — vmmd
  unreachable, mirrors `schedd_instance_stats_partial_errors_total`.
- Unit, MemStore / PgStore, migration up / down, OpenAPI spec-check, e2e API
  read, metal transfer test (guest → host veth → root counter → persisted
  `net_tx_bytes`).

## Consequences

- **Wire observation grows** (`vmmd.Stats`, `schedd.InstanceStatsRow`): additive
  monotonic cumulative counter. Schedd's poller skips rows where the value is
  nil.
- **Schema growth**: `usage_minutes` gains 16 bytes per row (two NULL-free
  bigints). At 1 minute × 100 instances × 50 apps = 80 KB per minute additive
  on top of the `cpu_usec` cost. Migration is `NOT NULL DEFAULT 0`.
- **The `tx_bytes` and `net_tx_bytes` columns are informational.** A future
  billing PR must define the unit (interface bytes vs IP bytes vs payload
  bytes) before pushing; this ADR freezes the unit as **interface bytes on
  root-side `vethHost.rx_bytes`**.
- **The sampler keeps a 30 s TTL on the schedd source.** If schedd is
  temporarily unreachable, no network observation is committed. Once the
  same cumulative counter returns, the durable checkpoint catches up the
  missing delta instead of treating the gap as zero. A counter reset is
  regression-safe and starts a new baseline without unsigned wraparound.
- **The OpenAPI / apid handler / CLI panels mirror `cpu_usec`.** The smoke
  test that proves "billable fields use the same query and code path as the
  invoice" must be updated to assert that the `tx_bytes` / `net_tx_bytes`
  fields are not part of the invoice.
- **The `pkg/api/limits.go` table is untouched.** No new limit field, no
  per-plan egress quota, no overage constant. The seam is the column, not the
  limit.

## Rejected alternatives

- **Billing integration in the same PR.** Out of scope per the explicit
  constraint and the owner's decision to defer egress pricing.
- **Use `nft -j list counters` as a byte meter.** It counts denied packets,
  not transmitted bytes — wrong cardinality, wrong unit.
- **Payload-byte accounting at the gateway only.** Strips tunnel / TLS
  framing and confuses the future billing unit; the gateway also misses
  non-HTTP egress (DNS, websockets, future surfaces).
- **Per-instance Prometheus label** (`vmmd_egress_bytes_total{instance}`).
  Violates the §6.2 fan-out invariant and the bounded-cardinality precedent
  set by `schedd_instance_cpu_pct{app,node}` rollups.
- **Sample the wrong side / direction of the veth.** `vethPeer.tx_bytes` in
  the netns is a different namespace and not visible to vmmd without a
  netns-enter syscall; reading `vethHost.tx_bytes` on root counts inbound
  (gateway → guest) bytes, which is not customer egress.
- **First-write-wins for sampled egress deltas.** Would silently under-count
  to 1/N of the true value at the sampler cadence (same hazard ADR-039 §2
  closed for `cpu_usec`).
- **Cgroup `net_*` stats.** Cgroup v2 nets are per-cgroup, not per-netns;
  aligning them to per-instance is non-trivial and offers no advantage over
  the kernel `/sys/class/net/<vethHost>/statistics/rx_bytes` file already
  there.

## Out of scope

- Egress pricing.
- Included egress quota per plan.
- Stripe / Paddle usage-record changes.
- Enforcement based on consumed bytes (the per-plan `tc tbf` cap is unchanged).
- Historical backfill (the migration default is 0).
- Builder / OCI control-plane egress.
- Multi-account egress rollups (per-account is already covered by
  `usage_minutes`; per-tenant dashboards are a separate ADR).

## Verification

- `make lint`, `make proto-check`, `make sqlc-check`, `make spec-check`.
- `go test ./...` — unit tests:
  - `pkg/fcvm/netstats/cache_test.go` (8 branches mirroring `cpustats`).
  - `pkg/meter/sampler_egress_test.go` (4 tests pinning the
    `egressDeltaForMinute` semantics).
  - `pkg/vmmdgrpc/stats_egress_test.go` (3 wire-shape tests).
  - `pkg/state/pgstore_append_usage_test.go::TestPg_AppendUsage_AddsTxBytesAndNetTxBytesOnConflict`
    (additive merge with non-zero deltas; asserts the persisted value via
    `UsageByMonth`, fixing the weak test at
    `pkg/meter/sampler_cpu_test.go:70-79`).
- `make test-metal` — `pkg/fcvm/netstats/cache_metal_test.go`:
  guest egress → `vethHost.rx_bytes` delta → `AppendUsage.tx_bytes` /
  `net_tx_bytes` end-of-test total equality.
- `make leakcheck` — no leaked netns / TAPs / veth leaves.
- `make e2e` — `cmd/e2e/egress_metering_test.go` smoke that calls
  `GET /v1/usage` and asserts the `tx_bytes` and `net_tx_bytes` fields are
  populated.

## Cross-references

- Spec §4.7, §7, §10, §12, §14, §17, Appendix A.
- ADR-039 (CPU-metering visibility) — exact precedent.
- ADR-016 (additive wire fields).
- ADR-031 / ADR-033 (egress allowlists).
- ADR-041 (migration slot reservations).
- ADR-042 / ADR-044 / ADR-045 (recent ADR format precedents).
