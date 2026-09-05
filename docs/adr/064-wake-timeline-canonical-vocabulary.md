# ADR-064 · Wake timeline: canonical vocabulary + customer-facing endpoint

- **Status:** proposed
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-08-02
- **Issue:** issue #517 PR-C — close AC2 (canonical wake timeline)
- **Decision:** Ship a single typed fan-out seam (`pkg/events.Platform`)
  with a canonical `wake.*` vocabulary, a customer-facing
  `GET /v1/apps/{slug}/wakes/{wake_id}/timeline` endpoint, and a
  partial jsonb expression index on `events.data->>'wake_id'`.

## Context

Issue #517 ("LOGGING: correlation, server-side filters, and gap
semantics") is split across four PRs. PR-A (`pkg/wire/logging.go`
+ `pkg/wire/grpcmetadata.go` + envelope propagation) merged as
PR #520 on 2026-08-01 and closed AC1 (correlation propagation).
PR-B (server-side filters + ring gap frames) merged as PR #524
on 2026-08-01 and closed AC3 + AC4. PR-C closes AC2:

> **AC2** — A cold wake has a queryable timeline with queue,
> admission, restore/cold-boot, guest resume, readiness, and
> proxy stages.

PR-A gave the platform a stable correlation envelope (six
canonical fields) that flows `gatewayd → schedd → vmmd`. PR-B
gave the customer app-log filter + gap surface. The wake
lifecycle itself was still **only inferable from a downstream
audit row** — there was no typed wake-stage signal, and the
operator / customer could not query "show me the timeline for
wake X" without a hand-rolled `SELECT * FROM events WHERE
data->>'wake_id' = X` against an unindexed jsonb.

The events table was already there (audit-log surface, IAM-4
ADR-035), but the wake-data lived in the jsonb `data` column
with no expression index. The wake_id was stamped by PR-A, but
the customer-facing read path was missing.

## Decision

### 1. Typed fan-out seam — `pkg/events.Platform`

Extend the existing `pkg/events` (currently `broadcaster.go` only
— in-process SSE pub/sub) rather than create `pkg/wake` /
`pkg/lifecycle`. The broadcaster's existing doc-comment
("Postgres LISTEN is the cross-process story; pkg/events is the
in-process wake-up") is the contract PR-C earns.

```go
// pkg/events/platform.go
type WakeEvent interface {
    Kind() string
    At() time.Time
    Subject() *string
    Payload() map[string]any
}

type Platform struct {
    actor       string
    store       state.Store
    log         *slog.Logger
    ops         Ops
    broadcaster BroadcasterIf
}

func (p *Platform) Emit(ctx context.Context, ev WakeEvent) { ... }
```

Concrete typed payload structs (`BootStarted`, `BootCompleted`,
`BootFailed`, `ParkStarted`, `ParkCompleted`, `Stalled`,
`Readiness200`, `ProxyFirstByte`, `QueueAccepted`, `Admitted`,
`BuildSucceeded`, `BuildFailed`, `DeployFailed`) implement the
interface. The Go compiler is the cheapest schema validator.

Rejected: `kind string + data map[string]any` (mirrors
`pkg/audit.Auditor.Emit` — schema-invisible at compile time);
`Stage` enum + ctx (hides the payload).

### 2. Canonical `wake.*` vocabulary

| Kind | Payload | Emit site |
|---|---|---|
| `wake.queue_accepted` | `{wake_id, app_id, request_id, queue_wait_ms}` | schedd `pkg/sched/engine.go` Wake Phase 1 + `pkg/sched/loop.go` cron boundary |
| `wake.admitted` | `{wake_id, app_id, request_id, account_id, plan, admitted_at}` | schedd admission gate |
| `wake.boot_started` | `{wake_id, app_id, instance_id, node_id, method, requested_at}` | schedd boot path + vmmd mirror in `pkg/vmmdgrpc/server.go::CreateFromSnapshot` |
| `wake.restore_breakdown` | `{wake_id, app_id, instance_id, chroot_ms, materialize_mem_ms, materialize_vmstate_ms, resolve_images_ms, stage_drives_ms, stage_snapshot_ms, helper_ms, start_jailer_ms, bind_tun_ms, load_snapshot_ms, resume_hook_ms, wait_ready_ms, total_ms}` | vmmd `pkg/fcvm/vmm.go::Restore` after successful snapshot readiness |
| `wake.boot_completed` | `{wake_id, app_id, instance_id, node_id, method, started_at, completed_at}` | schedd post-`RecordRuntime` |
| `wake.boot_failed` | `{wake_id, app_id, instance_id, node_id, method, reason, failed_at}` | schedd boot path alongside `wake_boot_error` audit row |
| `wake.readiness_200` | `{wake_id, app_id, instance_id, node_id, healthcheck_path, probe_count, elapsed_ms}` | vmmd `pkg/fcvm/vmm.go::waitReady` on the first 2xx probe |
| `wake.proxy_first_byte` | `{wake_id, app_id, request_id, instance_id, node_id, latency_ms}` | gatewayd `pkg/gateway/forwardproxy.go` Response Init frame `WriteHeader` |
| `wake.park_started` | `{wake_id, app_id, instance_id, node_id, started_at}` | schedd Snapshotting transition |
| `wake.park_completed` | `{wake_id, app_id, instance_id, node_id, started_at, completed_at, snapshot_id}` | schedd Snapshot success path |
| `wake.stalled` | `{wake_id, app_id, instance_id, node_id, reason}` | schedd watchdog path |
| `wake.build_succeeded` | `{app_id, deployment_id, image_digest, duration_ms}` | builderd `pkg/builderd/builderd.go` |
| `wake.build_failed` | `{app_id, deployment_id, image_digest, reason}` | builderd mirror |
| `wake.deploy_failed` | `{app_id, deployment_id, reason}` | apid `cmd/apid/audit_subscriber.go` rollback path |

**Legacy bare names preserved unchanged**: `state_transition`,
`wake_boot_error`, `park_snapshot_error`, `watchdog_timeout`,
`app.characterized`, `cron.fired`, `reaper_scale_down`,
`stateless.advisory`, `app.signed_image_accepted`,
`app.signature_missing`, `app.signature_invalid`. Add to ADR-035
§"Kind taxonomy": "Additive to existing audit kinds; do not
migrate legacy rows."

### 3. Customer-facing endpoint

`GET /v1/apps/{slug}/wakes/{wake_id}/timeline` is a sub-resource
of `/v1/apps/{slug}` (mirrors `/v1/apps/{slug}/logs`,
`/v1/apps/{slug}/metrics`, `/v1/apps/{slug}/wake`). The auth
gate mirrors `GET /v1/audit-events`.

Response: `WakeTimelineResponse{wake_id, app_id, events[],
next_cursor}`. Each `WakeTimelineEvent{at, kind, actor, data}`.
Order: `at ASC` (forward narrative).

Cross-account safety: the handler does `slug → state.Store.
AppBySlug` first, then verifies every events row's `data.app_id`
matches the resolved app. A row that mismatches is dropped
silently (forge-proof).

### 4. Partial jsonb expression index

```sql
-- migrations/00113_events_wake_id_idx.sql
CREATE INDEX IF NOT EXISTS events_wake_id_idx
  ON events ((data->>'wake_id'))
  WHERE data->>'wake_id' IS NOT NULL;
```

A naive `WHERE data->>'wake_id' = X` query would miss frames on
high-RPS apps (audit-events table can carry 10⁶+ rows / day /
account). The partial index bounds the index size to the
wake-envelope rows (1 per wake phase, ~13 per cold wake)
rather than the full audit-log volume.

### 5. Metrics

Two new pre-instantiated collectors in `pkg/wire.NewOpsMetrics`:

- `wake_phase_emitted_total{daemon,phase,result}` — CounterVec.
  `daemon` is the literal `wire.Daemon` name; `phase` is the
  kind substring after `wake.`; `result` is `ok` or `failed`
  (AppendEvent return). Pre-instantiated closed tuples on every
  daemon's `OpsMetrics` so the §12 panel never goes dark.
- `wake_phase_duration_seconds{phase,result}` — HistogramVec.
  Buckets: `[]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25,
  0.5, 1, 2.5, 5, 10, 30, 60}` — sized for the wake envelope
  (queue → admit <100ms; boot <30s; readiness <60s; proxy <5s).

The vmmd event-write histogram is named
`vmmd_wake_event_write_duration_seconds{phase,result}`. Its historical
`vmmd_wake_phase_duration_seconds` name collided with fcvm's execution-phase
histogram, which has different help text, labels, and buckets. Combining those
registries made the canonical scrape fail. The execution metric retains its
name; vmmd event-write queries must use the new name. Other daemons retain
their existing event-write metric names.

### 6. Best-effort emit semantics

The events Platform.Emit is best-effort (mirrors
`pkg/audit.Auditor.Emit` precedent): a failure to write the
`events` row logs `Warn` + increments the `result=failed`
counter, but never rolls back the upstream wake operation. The
canary runbook (`docs/runbooks/WakeTimelineEndpointRollout.md`)
documents the 24-hour observability ramp.

### 7. Cron dispatch boundary

`pkg/sched/loop.go` emits `wake.queue_accepted` at the cron
dispatch boundary (the same mint point that fires `cron.fired`).
The cron-side row joins to `cron.fired` via `request_id`.

### 8. Watchdog dual emission

The watchdog path keeps both `watchdog_timeout` (legacy audit
row, GDPR-export compatible) AND `wake.stalled` (typed timeline
row with structured payload). Customer-facing timeline surfaces
both joined by `wake_id`.

## Consequences

### Positive

- The customer-facing timeline is a single, schema-typed
  endpoint that surfaces every wake stage in order.
- The wake_id is the canonical join key across the existing
  audit-log surface (PR-A's envelope propagation) and the new
  timeline endpoint.
- The partial index bounds storage cost to the wake-envelope
  volume, not the full audit-log volume.
- The `pkg/events.Platform` seam is reuseable for future
  typed fan-out (PR-D's jailer/Firecracker stderr capture is
  the next adopter).

### Negative

- Adds 13 new event kinds to the events table. The audit-log
  volume grows by ~13 rows per cold wake. The partial index
  bounds the index size but the row volume is unbounded.
- The `wake.readiness_200` emit in `pkg/fcvm/vmm.go::waitReady`
  is the most load-bearing new code path — a regression there
  silently drops the terminal wake stage from the timeline.
- Best-effort emit semantics mean a wake that fails to write
  the timeline row is invisible to the customer. The §12
  `wake_phase_emitted_total{result="failed"}` counter is the
  load-bearing alert.

### Compatibility

- Legacy bare names (`state_transition`, `wake_boot_error`, ...)
  preserved unchanged. Do not migrate legacy rows.
- The new endpoint is additive — no existing route changes.
- The partial index is `CREATE INDEX IF NOT EXISTS` — safe to
  re-run on a database that already has the index.

## Alternatives considered

- **New `pkg/wake` package.** Rejected — the existing
  `pkg/events` doc-comment ("in-process wake-up") already
  describes the seam. Adding a new package would split the
  audit-log + wake fan-out logic across two packages.
- **`kind string + data map[string]any`** (mirrors
  `pkg/audit.Auditor.Emit`). Rejected — schema-invisible at
  compile time. The wake-timeline vocabulary is the canonical
  shape the customer-facing endpoint returns, so the type
  system is the cheapest validator.
- **`wake_id` as a filter on `GET /v1/audit-events`.** Rejected
  — the audit endpoint is `subject`-pinned with a 200-row
  over-read cap; `data->>'wake_id'` is unindexed and the
  `wake_id` lives in the jsonb `data` column, so a naive
  filter would miss frames on high-RPS apps. The new endpoint
  is a sub-resource of `/v1/apps/{slug}` (mirrors logs/metrics/
  wake).
- **Feature-flag rollout (`enable_wake_timeline`).** Rejected —
  the read path is bounded by the partial index size, not the
  audit-log size. The canary is therefore an observability ramp
  (see `docs/runbooks/WakeTimelineEndpointRollout.md`), not a
  feature-flag ramp.
- **OpenTelemetry compatibility.** Deferred — issue #518
  (independent workstream). The `wake.*` vocabulary is the
  canonical shape that an OTel exporter would map to spans.

## Critical files

- `pkg/events/platform.go` — Platform + Emit + WakeEvent interface.
- `pkg/events/wake.go` — kind constants + payload structs.
- `pkg/api/wake_timeline.go` — response types.
- `pkg/api/client.go` — `ListWakeTimeline` method.
- `cmd/apid/handlers_wake_timeline.go` — endpoint handler.
- `cmd/apid/audit_subscriber.go` — deploy-failed emit.
- `pkg/wire/metrics.go` — `wakePhaseEmitted` + `wakePhaseDur`.
- `migrations/00113_events_wake_id_idx.sql` — partial index.
- `cmd/e2e/wake_timeline_metal_test.go` — M5 §14 acceptance.
- `docs/runbooks/WakeTimelineEndpointRollout.md` — canary
  rollout.
