# ADR-20261018083536 · Customer log search and analytics over the unified ledger

- **Status:** proposed
- **Date:** 2026-10-18
- **Decision:** Expose the [ADR-213](213-durable-unified-log-events.md) `log_events`
  ledger as a customer search API: a filtered, keyset-paged event query with
  level and bounded text matching, plus a facet query that counts matches by
  source, level, status class, and top routes. Then widen what the ledger holds
  (runtime warn/error lines, build and deploy events) and let customers save
  searches and alert on match counts. Dark behind `FAAS_LOG_SEARCH_ENABLED`.
- **Why:** Gregale's log surfaces answer "what is happening now" well (the
  runtime ring, `--grep`, `--level`, local views, `--explain`), but not "show me
  every 5xx on `/checkout` in the last six hours, then count them by route".
  That is the core log workflow customers bring from Datadog. ADR-213 built the
  ledger for it and left the public query endpoint as a stacked change.
- **Consequences:** Reads stay app-scoped with `account_id` and `app_id` in the
  predicate (ADR-213 IDOR guard). Text search is a case-insensitive substring
  match, bounded to a 24 h window and a statement timeout, so it never scans
  more than one day of one app's partition range. Facets cap at a 7 d window.
  Runtime projection is deliberately lossy: only warn/error/fatal lines, rate
  capped per app, so the control-plane database grows with problems rather
  than with traffic; full output stays in the ring and the S3 archive.
- **Rejected alternatives:** A Loki-backed tenant API (ADR-213 already rejected
  making a product API depend on the operator logging plane). Indexing every
  runtime line in Postgres (unbounded growth on the control-plane node; Datadog
  prices this per GB for a reason). A trigram index for text search (write
  amplification on the hottest table for a query the window bound already
  keeps cheap). Server-side query language parsing (filters as query
  parameters map one-to-one onto indexed columns and are easier to bound).

## Slices

**A. Search and facets (this change).**

- `GET /v1/apps/{slug}/log-events` takes `since`/`until` (duration or RFC 3339;
  default the last hour), `source`, `level` (comma list), `route`, `status`
  (exact code or class `2xx`..`5xx`), `deployment_id`, `request_id`,
  `trace_id`, `q` (2–256 characters), `limit` (1–200) and `cursor`. It returns
  `LogQueryEvent` rows newest first with an opaque `next_cursor`.
- `GET /v1/apps/{slug}/log-events/facets` takes the same filters (no cursor)
  and returns the total, counts by source, level and status class, and the
  ten busiest routes.
- Windows: any search is clamped to the plan's ledger retention; text search
  and facets reject windows over 24 h and 7 d. Each query runs under a 5 s
  statement timeout and returns `log_search_timeout` when it trips.
- `gregale logs search` and `gregale logs facets` mirror the endpoints; the
  dashboard gains a log search page.

**B. Runtime lines.** vmmd projects runtime lines the ADR-746 classifier marks
warn or error (and structured lines whose level is warn or above) to apid over
a new internal gRPC `LogEvents` service; apid inserts `source=runtime`. Each
app gets a per-minute line budget; over budget, vmmd drops lines and emits one
system event per minute with the dropped count.

- vmmd observes each instance ring through the fcvm instance log observer,
  which carries the host-owned app, deployment and instance identity. The
  observer runs under the ring mutex, so it only classifies, charges the
  budget and enqueues without blocking (queue 2048); one goroutine sends
  batches of up to 100 every second. Unavailable or Unimplemented from apid
  pauses forwarding for a minute: a disabled or older apid costs one call a
  minute, not one per batch. `vmmd_runtime_log_events_total{outcome}` counts
  every line's fate. Gate: `FAAS_RUNTIME_LOG_EVENTS_ENABLED=1` on vmmd.
- `LogEvents` registers on the listeners that already carry `SpansWriter`
  (the single-box Unix socket and the split-box mTLS listener), and vmmd dials
  the guest-trace target. The slice adds no socket, certificate or unit.
- apid derives the account from the app (vmmd never names it), caps each
  account at 600 lines a minute across nodes, redacts with the ADR-096
  redactor (pattern names go in `fields.redactions`), and inserts. It accepts
  only while `FAAS_LOG_SEARCH_ENABLED=1`. A database failure fails the batch
  with Unavailable; `source_event_id` (`<instance>:<ring seq>`, or
  `budget:<app>:<minute>` for drop summaries) makes any replay converge.
- Budgets: 120 lines per app per minute per node, 4 KiB per line, in
  `pkg/api/limits.go`.
- `JailerVMM.WithLogCommitCallback` and `WithInstanceLogObserver` now add an
  observer instead of replacing the previous one. vmmd installed two commit
  callbacks, so the ADR-746 counter had silently displaced the MCP protocol
  metrics; both now observe every line.

**C. Build and deploy.** Deployment log lines and deploy state changes project
into the ledger as `build` and `deploy` events from the component that already
writes them, through apid.

**D. Saved searches and log alerts.** Account-scoped saved searches store a
filter set server-side (the CLI's local views keep working). An alert metric
counts a saved search's matches over the rule window, evaluated with the
facet query's bounds.

## Limits

Defined in `pkg/api/limits.go`: page size 200, text query 2–256 characters,
text window 24 h, facet window 7 d, 10 facet routes, 5 s statement timeout.
