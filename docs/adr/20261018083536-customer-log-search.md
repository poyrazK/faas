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
