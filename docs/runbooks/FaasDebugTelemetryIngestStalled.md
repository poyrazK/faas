# FaasDebugTelemetryIngestStalled

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `gatewayd_public_otel_spans_ingested_total{outcome="inserted"}`
(counter, single label `outcome` ∈ {inserted, rate_limited,
db_error, shape_invalid}).
ADR: ADR-127 PR-D (production debugger OTel spans writer).
Severity: warn on `FaasDebugTelemetryIngestStalled` (`db_error`
outcomes and no `inserted` outcome over 10m, held for 15m). Warn, not
page — the customer's app keeps running; only the debugger's OTel
summary view is going dark. An idle endpoint (no exports at all) and
customer-side `rate_limited` / `shape_invalid` outcomes do not fire it.

## Symptom

`gatewayd-public`'s `POST /v1/otel/v1/traces` endpoint is receiving
spans, every write fails with `db_error`, and no `inserted` outcome
has been recorded for ≥15 minutes. The OTLP
writer (ADR-127 PR-D) ships customer OTel spans through a
30s-coalesce-then-flush loop into
`request_telemetry.spans_summary jsonb`. A stall means the
debugger's per-request span view (PR-D's Stage 5 CLI smoke +
PR-B's regression banner drill-down) is going dark, but the
gateway's mainline function execution is unaffected.

- **`gatewayd_public_otel_spans_ingested_total{outcome="inserted"}`**:
  counter, increments once per successful flush-loop write to
  `request_telemetry.spans_summary` (per OTLP batch that passes
  auth + rate-limit + shape checks AND lands in the DB).
- **`gatewayd_public_otel_spans_ingested_total{outcome="rate_limited"}`**:
  counter, increments when the customer's per-account token
  bucket is exhausted (same pool that PR-B uses for
  IncrementRequestTelemetry — `pkg/ratelimit/peraccount.Limiter`).
  A spike here means a runaway customer's debug-telemetry quota
  is the cause, not the gateway.
- **`gatewayd_public_otel_spans_ingested_total{outcome="db_error"}`**:
  counter, increments when the apid `WriteSpansSummary` gRPC RPC
  fails. The likely cause is the apid daemon being down OR
  Postgres being unreachable from apid. Cross-check
  `apid_request_telemetry_recorded_total` — if THAT counter is
  also stalled, the bug is upstream of the OTLP writer.
- **`gatewayd_public_otel_spans_ingested_total{outcome="shape_invalid"}`**:
  counter, increments when the OTLP body fails the
  `decodeExportTraceServiceRequest` parser or the
  ResourceSpans/ScopeSpans/Span shape validator. A sustained
  shape_invalid rate is a customer's broken OTel SDK — not a
  gateway problem.

## Likely causes (most common offenders)

1. **Quiet customer base** (false positive on a small fleet):
   no customer is actively exporting OTel spans in the 15m
   window. The Hobby/Pro debug telemetry opt-in is recent
   (PR-D added it) so adoption is growing. Check
   `gatewayd_public_otel_spans_ingested_total` directly — a flat
   zero across all four outcome labels for >15m confirms the
   fleet is quiet. No action required; bump the alert's `for:`
   to ≥30m if the noise is unbearable.
2. **apid down**: the `WriteSpansSummary` gRPC RPC on
   `/run/faas/otel_spans_writer.sock` is unreachable. The
   gateway logs `otel_spans: apid RPC failed` for every flush
   tick. Check `ss -tlnp | grep apid` on the gatewayd-public box
   — the OTLP writer daemon must be up. If apid is down, the
   rest of the platform is also degraded; check
   `apid_request_telemetry_recorded_total` for the canonical
   health signal.
3. **Postgres unreachable from apid**: the
   `UpdateSpansSummary` UPDATE lands on
   `request_telemetry.spans_summary` via `pkg/state.UpdateSpansSummary`
   (PR-D Stage 1). A Postgres outage shows as `db_error` outcome
   on the gateway metric and `apid_request_telemetry_recorded_total`
   stalls simultaneously. Cross-check `pg_isready` on the
   control-plane node.
4. **Per-account rate-limit storm**: a customer's OTel SDK is
   exporting at >`DebugTelemetryRequestsPerMinute` (Hobby=1000,
   Pro=10000, Scale=50000). The bucket exhausts and every flush
   returns `rate_limited`. The customer's app keeps running
   (their functions execute normally) but their spans_summary
   is empty. Check `gatewayd_public_otel_spans_ingested_total`
   per-outcome — a sustained `rate_limited > 0` with
   `inserted ≈ 0` confirms. Drill-down: `topk(5,
   rate(gatewayd_public_otel_auth_failures_total[5m]))` (the auth
   metric is per-account via the AuthenticateKey RPC).
5. **Drain / shutdown**: gatewayd-public is in the middle of a
   graceful drain — the drain tracker blocks new flushes while
   in-flight HTTP finishes. A >15m drain is a bug; check
   `pkg/gateway/drain` for stuck in-flight requests.
6. **PR-D cold start** (only fires in the first 24h after the
   Debugger UX v1 PR merges): no customer has instrumented
   yet, so the fleet is quiet. The alert will page on
   noise — set `for:` ≥30m for the first week post-merge.

## Triage (escalate by severity)

1. **Check the per-outcome breakdown**:
   `rate(gatewayd_public_otel_spans_ingested_total[5m]) by (outcome)`.
   - All four outcomes ≈ 0 → quiet fleet, no action.
   - `inserted ≈ 0` + `rate_limited > 0` → customer quota
     storm; see likely cause #4.
   - `inserted ≈ 0` + `db_error > 0` → apid or Postgres
     unreachable; see likely causes #2 and #3.
   - `inserted ≈ 0` + `shape_invalid > 0` → customer's OTel
     SDK is broken; open a customer ticket, not a paging
     incident.
2. **Check the gateway logs**: `journalctl -u
   gatewayd-public --since "30 min ago" | grep otel_spans`.
   The four log lines that surface here are
   `otel_spans: apid RPC failed`, `otel_spans: rate_limited`,
   `otel_spans: shape_invalid`, and `otel_spans: db_error`.
   Each carries the trace_id so you can correlate against
   `request_telemetry` directly.
3. **Check the apid side**: the
   `apid_request_telemetry_recorded_total{outcome="inserted"}`
   counter should NOT be stalled alongside the gateway metric —
   if both are zero, the bug is upstream of both (gatewayd-internal
   request_telemetry publisher stalled). The publisher flushes
   every 5s; a >15m stall is its own P1 incident
   (`FaasRequestTelemetryPublisherStalled` would be a useful
   future alert).
4. **Cross-check the upstream gateway signal**:
   `gatewayd_public_request_duration_seconds_count{class=~"5.."}`
   should be incrementing — if it's also stalled, the gateway
   is broken, not just the OTLP path.

## Mitigation

1. **Quiet fleet**: no action. The alert resolves itself when
   the next customer exports a batch. Bump `for:` ≥30m if
   the noise is unbearable during the PR-D adoption ramp.
2. **apid down**: restart apid (`systemctl restart apid`).
   The `/run/faas/otel_spans_writer.sock` socket is recreated
   on startup by `wire.ListenOrRecreateByName` (same helper
   PR-B uses for the auth.sock listener).
3. **Postgres unreachable**: page the database on-call.
   The OTLP writer is downstream of the database; fixing
   the database restores all four outcomes.
4. **Customer quota storm**: bump the customer's plan OR
   document the customer's SDK bug. Per-account token
   bucket caps are not adjustable mid-window; the customer
   has to wait for the bucket to refill (DebugTelemetryRequestsPerMinute
   refill rate). For Hobby/Pro stuck at quota, the customer
   can downgrade the SDK's export frequency.
5. **Drain stuck**: investigate the drain tracker
   (`pkg/gateway/drain`). A drain should complete in <30s;
   >15m is a bug. Restart gatewayd-public to force-cancel.

## When NOT to escalate

- The alert fires during the **first 24h after Debugger UX
  v1 merges**. Adoption is slow; the fleet is quiet. Set
  `for:` ≥30m for the first week post-merge; revert after
  customer adoption climbs.
- The alert fires during a **planned gatewayd-public drain**
  (config push, schema migration, debug endpoint offline).
  The drain tracker blocks new flushes; the alert resolves
  itself when the drain completes. A >30m drain is a bug;
  investigate `pkg/gateway/drain`.
- The alert fires when **all four outcomes are zero**. The
  fleet is quiet; no action required. Page only when the
  per-outcome breakdown shows non-zero `db_error` or
  `shape_invalid`.