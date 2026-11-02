# ADR-127 PR-D — Production debugger OTel spans writer

## Context

ADR-127 §5 (`docs/adr/127-production-debugger.md`) committed a
`spans_summary jsonb` column on `request_telemetry` (PR-A) but
the writer that populates it was never built. The OTel
endpoint was *advertised* in §5 but did not exist on any
daemon. Without a writer, PR-C's LLM prose synthesis has
nothing to read; without PR-D, the debugger ships a column
that is always NULL.

PR-D closes the gap by:

1. Hosting `POST /v1/otel/v1/traces` on `gatewayd-public`
   (the same daemon as `pkg/gateway/trace_handler.go`'s
   reader; CLAUDE.md ownership).
2. Auth via a new `AuthenticateKey` gRPC service on apid
   over `/run/faas/auth.sock` (unix socket, DAC 0660 group
   `faas`). The handler hashes the bearer token (sha256) and
   calls apid's `store.AuthenticateKey` (sha256 lookup is
   O(1) on the existing `api_keys.key_sha256` unique index).
3. Per-account rate limit on the OTLP POST itself
   (`DebugTelemetryRequestsPerMinute`), shared with PR-B's
   `IncrementRequestTelemetry` via the extracted
   `pkg/ratelimit/peraccount.Limiter`. Refill = cap/60
   tokens/sec; capacity = full cap (1-min burst). plan-cap=0
   disables.
4. Top-N slowest-spans truncation, N =
   `limits.DebugTelemetrySpansPerTrace` (Hobby=50,
   Pro=200, Scale=1000). Sort key: `(end - start)
   unix-nano` desc — PR-C's prose needs diagnostic-relevant
   slow spans, not the chatty parent server span.
5. Coalesce-then-flush in `gatewayd-public`:
   `sync.Map[trace_id]*accumulator` deduplicates spans
   arriving in multiple OTLP batches within a 30s window
   keyed by `(span_id, end_time_unix_nano)`. One
   `WriteSpansSummary` gRPC per (trace_id, flush window).
6. The writer — a new `WriteSpansSummary` gRPC RPC on apid
   over `/run/faas/otel_spans_writer.sock`. Validates
   trace_id regex (matches the DB CHECK), JSON-validates
   summary, resolves account_id, takes a token from the same
   per-account bucket, then runs:

   ```sql
   UPDATE request_telemetry
   SET spans_summary = $2::jsonb
   WHERE trace_id = $1
     AND received_at >= now() - interval '24 hours'
   ```

   The 24h window matches the existing partial index
   `request_telemetry_trace_idx` selectivity.

## Decision

### Auth surface: loopback gRPC over `/run/faas/auth.sock`

**Why:** Mirrors PR-B's `IncrementRequestTelemetry`
(`cmd/apid/main.go:2243-2265` `runRequestTelemetryServer`).
Bearer middleware couples auth to `pkg/state.Store` and
would create a new reverse-dependency (gatewayd-public →
store). Loopback RPC keeps the ownership invariant
("apid is the only writer to customer-intent tables")
and the latency budget (sub-ms per RPC on loopback unix
socket).

**Wire discipline:** unix socket, DAC 0660 group `faas`,
insecure gRPC credentials over trusted local transport.
No client certs in v1.0.

### Rate limit: PER-REQUEST, not per-span

**Why:** PR-B chose per-request semantics. A 1000-span
Hobby batch should consume 1 token (one POST = one rate
limit slot), not 1000. The bucket pool is shared with
PR-B's `IncrementRequestTelemetry` — both paths hit the
same `pkg/ratelimit/peraccount.Limiter` keyed by
account_id.

**Per-account caps come from `api.MustLimitsFor(plan)`:**

- `DebugTelemetryRequestsPerMinute` — bucket capacity
- `DebugTelemetrySpansPerTrace` — top-N truncation cap
- `DebugTelemetryEnabled` — plan gate (returns 402)

Cache TTL is 60s; miss falls back to `api.PlanScale`'s
caps (most permissive) so a fresh account's first
OTLP POST succeeds while the cache warms.

### Truncation policy

Top-N slowest spans by `(end - start) unix-nano`. Within
ties, deterministic by span_id ascending. N comes from
`limits.DebugTelemetrySpansPerTrace`.

### Write path: UPDATE, not INSERT

`request_telemetry` rows are written by PR-A's
`IncrementRequestTelemetry` recorder. PR-D enriches
existing rows via UPDATE, scoped to the last 24h window.
No INSERT path, no new schema, no new constraint.

### Coalesce window

`sync.Map[trace_id]*accumulator`. Each accumulator
stores a `(span_id, end_time_unix_nano)` dedupe set so
the same span arriving in two OTLP batches doesn't
double-count. Flush loop ticks every
`FAAS_OTEL_FLUSH_INTERVAL` (default 30s), drains
entries older than `interval * 2`, and ships them via
`WriteSpansSummary`. Bounded retry (default 3) per
entry; loop never panics on a wedged connection.

### Kill-switches (env)

| Variable | Default | Effect |
|---|---|---|
| `FAAS_OTEL_FLUSH_INTERVAL` | `30s` | Flush cadence |
| `FAAS_AUTH_RPC_ENABLED` | `true` | Disable apid `AuthenticateKey` service (gateway returns 503) |
| `FAAS_OTEL_SPANS_WRITER_ENABLED` | `true` | Disable apid `WriteSpansSummary` service (gateway stops flushing) |

All three are operator-only env vars; documented in
`docs/runbooks/otel-spans.md` (PR-C follow-on).

### Metrics (PR-D adds 4)

- `gatewayd_public_otel_spans_ingested_total{outcome}` —
  outcome ∈ {inserted, rate_limited, db_error,
  shape_invalid}
- `gatewayd_public_otel_spans_truncated_total` —
  fires when input > cap
- `gatewayd_public_otel_auth_failures_total{reason}` —
  reason ∈ {unauthenticated, plan_disabled, internal}
- `otel_spans_writes_total{outcome}` — apid-side mirror
  (outcome ∈ {inserted, rate_limited, db_error})

Names match §12 spec; dashboards depend on exact names.

### CLI smoke: `gregalectl debug otel-smoke`

Operator-side end-to-end verification at ship time.
POSTs a hand-crafted 3-span `ExportTraceServiceRequest`
to `http://127.0.0.1:8080/v1/otel/v1/traces` and
asserts 200 + `accepted_spans==3`. Used after PR-D
merges to verify the writer path end-to-end before
PR-C synthesizes from `spans_summary`.

## Why now

- PR-C is BLOCKED on PR-D (the LLM prose synthesis
  reads `spans_summary`; without PR-D it's always NULL).
- ADR-127 §5 was a forward commitment — the column is
  there, the writer is the missing piece.
- PR-B shipped today with regression cron + dashboard
  + CLI + replay all reading the column; without PR-D,
  the dashboard shows `spans_summary: null` for every
  row.

## Consequences

**Positive:**

- PR-C unblocks; the LLM synthesis layer can now read
  per-trace span summaries.
- Existing PR-B surfaces (regression cron, dashboard,
  replay) immediately start showing real span data on
  every request the customer tagged with traceparent.
- Per-account rate limit + plan gate + truncation make
  the endpoint safe to ship publicly without DOS risk.

**Negative / risk:**

- 4 MiB OTLP body cap is a v1.0 trade-off — large
  customers running distributed tracing with >4000 spans
  per request must upgrade to Pro (200 cap) or batch
  their SDKs. Documented in PR-C follow-on runbook.
- sha256 lookup is O(1) on the existing index but the
  `api_keys` table grows with customer count; no
  `otel_ingest_keys` rotation table is introduced in
  PR-D. If API key churn hits a threshold, PR-D.1
  follow-on adds the rotation table at slot 488.

## Rejected alternatives

- **Bearer middleware chain on `traceMux`** — couples
  gatewayd-public to `pkg/state.Store`, breaks
  ownership invariants.
- **Distributed Postgres-backed rate limiter** —
  PR-B chose in-process token bucket; matching the
  same regime avoids two rate-limit postures per
  customer.
- **Flush-at-end UPDATE** — OTLP is push-based; no
  "request end" signal exists.
- **Schema migration for `spans_summary`** — column
  shipped in PR-A (`migrations/00427_request_telemetry.sql:108`).
- **Schema migration for `otel_ingest_keys`** —
  deferred to PR-D.1; sha256 on `api_keys` is O(1)
  at current scale.

## Neighbors

- ADR-127 PR-A (`docs/adr/127-production-debugger.md`)
  — data plane shipped; this PR populates the
  writer-only side.
- ADR-127 PR-B (PR #1085) — consumer surface shipped
  today; PR-D populates the column PR-B reads.
- ADR-127 PR-C (BLOCKED) — LLM synthesis. Reads
  `spans_summary`; unblocks the day PR-D merges.

## Critical files

**New (12):**

- `pkg/ratelimit/peraccount/peraccount.go` + `_test.go`
- `api/proto/onebox/faas/apid/v1/auth.proto` (+ generated)
- `api/proto/onebox/faas/apid/v1/spans_writer.proto` (+ generated)
- `cmd/apid/grpc_server_auth.go` + `_test.go`
- `cmd/apid/grpc_server_spans_writer.go` + `_test.go`
- `pkg/apidgrpc/auth_client.go`
- `pkg/apidgrpc/spans_writer_client.go`
- `pkg/gateway/otel_spans_handler.go` + `_test.go`
- `pkg/gateway/spans_accumulator.go`
- `pkg/gateway/spans_accumulator_flush.go`
- `cmd/gregalectl/commands_debug.go`
- This ADR.

**Modified (8):**

- `cmd/apid/main.go` — `runAuthServer` +
  `runSpansWriterServer` helpers
- `cmd/apid/grpc_server_request_telemetry.go` —
  replace inline rate limiter with
  `*peraccount.Limiter`
- `cmd/gatewayd-public/main.go` — extend `traceMux`,
  start flush loop, MaxSpans closure
- `pkg/state/queries.sql` — append `UpdateSpansSummary`
  query
- `pkg/state/pgstore.go` — add `UpdateSpansSummary`
  wrapper
- `pkg/state/pgstore_request_telemetry_test.go` —
  append round-trip test
- `pkg/wire/metrics.go` — 4 new counters + getters
- `cmd/gregalectl/main.go` + `cli_meta.go` — wire
  `debug` dispatcher + manifest entry

## PR-D follow-ons (NOT this ADR)

- **PR-C**: LLM synthesis layer. Takes regression row
  + `spans_summary` + linked mirror invocation, emits
  prose. Unblocks the day PR-D merges.
- **gzip on OTLP POST body** — only if metrics show
  >100 KiB average body.
- **`otel_ingest_keys` rotation table** — only if
  customer API token churn hits a threshold.
- **Trace-id race with TraceRing** — gateway's own
  TraceRing spans do NOT land in `spans_summary`
  (privacy: gateway-side spans carry the request body).
- **PR-D.1 schema migration** — if `otel_ingest_keys`
  becomes necessary, real migration at slot 488 +
  `reserve_slot.sql` at 489 (standard fence pattern).

## Amendment (2026-11-01): tracing tenancy

The 2026-10-31 logs/traces audit (T4–T6, T8) found that the writer merged
spans by `(trace_id, account_id)` alone, where `account_id` is the app
creator's storage namespace, not the tenant. One trace id reached every app
that creator made, company apps included; the OTLP endpoint accepted any key;
and plans were the creator's, not the app's owner's.

- **Rows a flush may touch.** `WriteSpansSummaryRequest` gains optional
  `app_id`, `org_id` and `customer_credential` (fields 4–6; empty keeps the
  account-wide merge, so producers and apid release independently). The
  in-guest bridge, service bindings and outbound integrations name the
  calling app; the UPDATE then touches only that app's rows. A key bound to a
  company merges into the company's apps, whoever created them. A customer
  key's spans reach a company app only while its holder is an owner, admin
  or developer there, so a creator who left cannot write its evidence.
- **OTLP keys.** `AuthenticateKey` refuses a key without `admin`,
  `deploy:write` or `metrics:write` (`PermissionDenied` → 403), refuses a
  company key whose holder may no longer deploy there, and answers
  `NotFound` (→ 404) while the `tracing` capability is not released to the
  key's subject — the same decision apid's capability gate makes for
  `GET /v1/account/traces`. The response names the key's app or company.
- **Plans.** Admission, the spans-per-trace bound and the rate bucket come
  from the app's resource owner (`state.AppResourceAccount`), the owner
  request-telemetry retention already keys on through
  `app_resource_accounts`. A company that owns its resources draws from its
  own bucket. Resolutions are cached for a minute.
- **Separate span bucket.** Decision 3's shared bucket let enabling tracing
  crowd out request evidence. Span writes and in-guest exports now draw from
  their own per-tenant pool at the same `DebugTelemetryRequestsPerMinute`
  rate; request rows keep theirs.
- **Ingest gate.** In-guest exports are refused (`disabled`) while the
  `tracing` capability is off for the app's resource owner.
