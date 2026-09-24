# ADR-133 · Traffic mirroring runtime dispatch (PR-A3 follow-on)

- **Status:** proposed
- **Date:** 2026-08-27
- **Issue / PR:** [#72](https://github.com/poyrazK/faas/issues/72), PR-A3
- **Supersedes / amends:** none (first ADR for the runtime
  dispatch half; PR-A1's storage half and PR-A2's CRUD surface
  shipped without a dedicated ADR — both reference issue #72
  in code comments; this ADR consolidates the full surface)
- **Decision:** Wire the runtime half of traffic mirroring so a
  customer's `mirror_rule` actually fans out a percentage of
  live traffic to a shadow VM and writes a per-invocation ledger
  + hourly rollup. Six atomic commits; closed-set metric
  vocabulary; per-rule concurrency cap; detached-ctx dispatch
  goroutine; Postgres-backed rollup + retention sweep.

## Context

PR-A1 (issue #72 / ADR-125 / PR-A1, head `7cc2a573a`,
SHIPPED 2026-08-22) shipped the **storage half** of traffic
mirroring: schema (`mirror_rules`, `mirror_invocation_results`,
`instances.mode`), 8 store methods, sampler / reaper
`mode='mirror'` skip, plan caps.

PR-A2 (issue #1071, MERGED 2026-08-27) shipped the **customer-
facing CRUD surface**: 6 apid HTTP routes under
`/v1/apps/{slug}/mirrors`, 6 CLI verbs, 5 DTOs, 7 error
sentinels, `kind="mirror"` notify discriminant.

After A2 lands, a customer can `POST /v1/apps/{slug}/mirrors`
and see the rule via `GET`, but **no traffic is actually
mirrored yet** — the gateway ignores the rule, the schedd
never stamps `mode='mirror'`, the ledger has no writers. The
feature is dormant. PR-A3 turns the dormant feature live.

## Decision

Six atomic commits, each independently reviewable in ~10 min:

1. **`feat(gateway): mirror picker + cache`** — `Backend.LookupMirrorRules(ctx, appID)` plus a per-app cache on `PGBackend` (sibling to the `weights` map), refreshed by the existing `kind="mirror"` pg_notify arm.

2. **`feat(schedd): proto widening + AdmitMirrorInstance + per-rule slot cap`** — appenditive `bool is_mirror = 5` on `WakeRequest` + `AdmitInstanceRequest` (ADR-016), schedd `Engine.AdmitMirrorInstance(ctx, rule)` stamping `mode='mirror'` on the new `instances` row (column + CHECK from PR-A1's migration 00385), in-memory per-rule concurrency cap (default 5) bounded by `ErrMirrorSlotAtCapacity`.

3. **`feat(gateway): mirror redact + dispatch goroutine + handler fanout + metrics`** — `pkg/gateway/mirror_redact.go` (header strip + classify), `pkg/gateway/mirror_dispatch.go` (detached-ctx goroutine), post-Pick fan-out block in `pkg/gateway/handler.go`, three `gateway_mirror_*` metrics registered in `NewMetrics`, `kind="mirror"` notify branch in `cmd/gatewayd-internal/backend.go::handleInvalidation`.

4. **`feat(state): mirror_invocation_summary rollup + retention sweep`** — `migrations/00515_mirror_invocation_summary.sql` (PRIMARY KEY `(rule_id, hour_bucket)`, additive-merge UPSERT), `pkg/mirror/rollup.go` (`RollupOnce` / `SweepOldLedgerRows` / `RollupLoop`), wired into `cmd/schedd/main.go` next to the scale-up triggers.

5. **`feat(e2e): traffic_mirror_e2e_test`** — three Postgres-backed e2e pins for the rollup + sweep contracts.

6. **`docs(adr): ADR-133 PR-A3 follow-on`** — this document.

### D11. Mirror picker shape

Per-app cache lives on `PGBackend` next to the deployment-weight
table (`pkg/gateway/pgbackend.go::mirrorMu` + `mirrorRules
map[string][]state.MirrorRule`). Read path is cache-only —
`LookupMirrorRules` does NOT call the store. Refresh is the
only writer, driven by the `kind="mirror"` notify arm in
`handleInvalidation`. The hot path pays one `RWMutex.RLock` +
a map lookup; the notify arm pays the store read.

Rule cardinality is bounded by `Limits.MirrorTargetsPerApp ≤ 3`
so the cache size is closed-set at A3 launch. No eviction
policy required (slice replacement is sufficient).

### D12. Detached-ctx dispatch goroutine

ADR-098 discipline. The goroutine derives its own context from
`context.Background()` with a `MirrorMaxLifetimeSeconds=5`
budget so the customer's request cancellation never reaches
the mirror. A wedged mirror VM is bounded by 5s of CPU + RAM
on the gateway, not the customer. Customer response is on
the wire before `dispatchMirror` even starts.

No panic recovery — matches the `WakeGate` leader contract
(`pkg/gateway/gate.go:172-175`): a panic propagates to
`slog.Panic` and aborts the daemon. The deploy gate catches
it; no customer data is lost.

No span context propagation — matches the wake-coord precedent
(`pkg/sched/wake_coord.go:1276-1281`). Logs carry `rule_id` +
`app_id` + `mirror_deployment_id` for ops correlation.

### D13. Result classification

Four flags drive the ledger row + metric increment:

- `statusDiff`: src status != mirror status
- `schemaDiff`: `sha256(strippedMirrorBody) != sha256(strippedSrcBody)`
- `bodyDiff`: same predicate as schemaDiff (field exists distinct for forward-compat with JCS semantic diff)
- `crashed`: `mirrorStatus == 0` (timeout / transport error) OR `mirrorStatus >= 500`

A3 ships byte-equal diff via per-comparison HMAC-SHA-256. JCS schema-hash body
diff is an ADR-124 §Follow-on. The four-flag shape is
stable, so the dashboard chip doesn't need to change when
semantic diff lands.

### D14. Closed metric vocabulary

`gateway_mirror_dispatched_total{app_id, rule_id, result}`
emits exactly one of:

- `ok` — mirror returned 2xx with body match
- `mirror_5xx` — mirror returned 5xx
- `status_diff` — statuses differ (informational; also implies body diff unless src also failed)
- `body_diff` — statuses match, bodies differ
- `cap_at_max` — per-rule slot cap fired before admit
- `sched_error` — schedd errored other than cap-at-max
- `mirror_roundtrip_error` — round-trip transport error
- `build_request_error` — request body read / header build failed

`rule_id` cardinality is bounded by `Limits.MirrorTargetsPerApp`
≤ 3 per app. `app_id` cardinality matches the rest of the
gateway metrics. Dashboard readers and alerts MUST treat
unknown `result` values as a bug.

### D15. Rollup + retention sweep shape

`mirror_invocation_summary` rolls up the raw
`mirror_invocation_results` table on the (rule_id, hour_bucket)
PRIMARY KEY. The UPSERT is **additive-merge**:

```sql
ON CONFLICT (rule_id, hour_bucket) DO UPDATE SET
    total_invocations = mirror_invocation_summary.total_invocations + EXCLUDED.total_invocations,
    ...
```

Re-running the rollup on a partially-collected hour ADDs to
the existing count, not overwrites (contrast `usage_daily`
which sums over a full day window and overwrites). The raw
ledger is append-only so the running sum is the monotonic-
correct answer.

Retention sweep (`SweepOldLedgerRows`) deletes raw rows older
than `DefaultLedgerRetention` (7 days). The dashboard chip is
unaffected because `mirror_invocation_summary` already
preserves the per-hour counts.

`RollupLoop` runs both halves inside one goroutine on the
default 5-minute cadence (matches `pkg/meter/rollup.go`).
Errors are logged Warn and retried on the next tick — a
persistent failure surfaces as a flood of WARN logs an
operator can alert on.

## Out of scope (deferred to A4+ per ADR-124 §Follow-ons)

- Multi-target mirror (N candidates per source).
- Dashboard widget: mirror drift over time.
- Mirror across deployments on different apps.
- Mirror across regions.
- Auto-promote canary on zero diff over rolling window.
- JCS schema-hash body diff (A3 ships byte-equal).
- Cross-process per-rule concurrency cap (single-process schedd).
- Retention summary archive (e.g. `mirror_invocation_summary_archive` after 90d).

## Acceptance criteria

1. `TestStrippedHeaders_AlwaysStripped` — outgoing mirror request has zero of `{Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization, WWW-Authenticate}` headers.
2. `TestAdmitMirrorInstance_StampsMode` — after `AdmitMirrorInstance`, the new `instances` row has `mode='mirror'`.
3. `TestMirrorSlot_Cap` — 6 rapid admissions on a cap=5 rule → 5 succeed, 1 returns `ErrMirrorSlotAtCapacity`.
4. `TestE2E_MirrorRollup_AggregatesByRuleHour` — 5 ledger rows in 1 hour → rollup writes 1 summary row with `total_invocations=5`.
5. `TestE2E_MirrorSweep_DeletesOnlyStaleRows` — 3 rows aged 8d + 2 rows aged 1d → sweep deletes 3, leaves 2.

## Risks + mitigations

| Risk | Mitigation |
|---|---|
| Per-rule cap fires before wake → customer sees mirror not firing | Ledger entry written with `result=cap_at_max` + metric incremented; ops can lift the cap or split the rule. D14 documents the cap (5) explicitly. |
| Mirror goroutine panics → daemon crash | Match ADR-098 contract: "ensure never panics". Explicit `if err != nil { ...; return }` after every fallible call. Panic surfaces via `runtime.Goexit`, no customer data lost. |
| `kind="mirror"` discriminator collision with traffic-split's `kind="traffic"` | Both flows write the SAME channel (`NotifyDeploymentChanged`) but consume DIFFERENT discriminators. The gateway subscriber arms branch on `kind`. Wire-shape parity pinned by `cmd/gatewayd-internal/backend_test.go::fakeInvalidator.mirrorRefreshed`. |
| `gateway_mirror_dispatched_total{rule_id}` label cardinality unbounded | `rule_id` cardinality is bounded by `Limits.MirrorTargetsPerApp` ≤ 3 per app, so label set is closed at A3 launch. |
| Edge case: MirrorRoundTripper times out at exactly MirrorMaxLifetimeSeconds | Detached-ctx budget kills the goroutine cleanly; metric records `result=mirror_roundtrip_error`. Customer response was on the wire seconds ago. |

## Verification

```bash
make test PKG=./pkg/gateway/...    # mirror picker + redact + dispatch
make test PKG=./pkg/sched/...      # admit + slot cap + stamping
make test PKG=./pkg/mirror/...     # rollup + retention sweep
make test PKG=./pkg/state/...      # CreateInstanceWithMode overload
make proto-check                   # additive widening discipline
make migrations-check              # slot 00430 + fence 00431
make test-pg                       # e2e (cmd/e2e/traffic_mirror_e2e_test.go)
```
