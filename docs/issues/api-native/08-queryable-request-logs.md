# Sub-issue #08 — Queryable request logs

Parent: [README.md](README.md)

## Problem

Gregale today exposes:

- `GET /v1/apps/{slug}/logs` — app stdout/stderr SSE stream with grep,
  since, and severity filters. Shape at `pkg/api/logs.go:12-23`,
  cursor at `:84-126`. Backing store is a per-instance ring buffer per
  ADR-043 (`docs/adr/043-app-logs-stream.md:5-18`).
- No general HTTP request envelope store.

The audit's workflow ("1 endpoint with abnormal error rate") requires
querying historical requests by `(path, status, time, app, consumer)`.

## Proposal

New `request_logs` table:

```sql
CREATE TABLE request_logs (
  id              BIGSERIAL NOT NULL PRIMARY KEY,
  account_id      UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  consumer_id     UUID NULL REFERENCES consumer_keys(id) ON DELETE SET NULL,
  deployment_scope TEXT NULL,                          -- ADR-098 amendment
  method          TEXT NOT NULL,
  path            TEXT NOT NULL,                       -- raw, capped 512 chars
  route_label     TEXT NOT NULL,                       -- bounded per ADR-093
  status          INT NOT NULL CHECK (status BETWEEN 100 AND 599),
  bytes_in        BIGINT NOT NULL DEFAULT 0,
  bytes_out       BIGINT NOT NULL DEFAULT 0,
  latency_ms      INT NOT NULL CHECK (latency_ms >= 0),
  cold_start_ms   INT NULL,
  request_id      UUID NOT NULL,
  started_at      TIMESTAMPTZ NOT NULL,
  finished_at     TIMESTAMPTZ NOT NULL,
  user_agent_hash BYTEA NULL                           -- SHA-256, no raw UA
);
CREATE INDEX ON request_logs (app_id, started_at DESC);
CREATE INDEX ON request_logs (account_id, path, status, started_at DESC);
CREATE INDEX ON request_logs (consumer_id, started_at DESC) WHERE consumer_id IS NOT NULL;
```

### Write path

`gatewayd-internal` already buffers the request envelope for ADR-093
metrics emission (`pkg/gateway/handler.go:4762-4769`,
`:4816-4822`). Add a fire-and-forget insert at request completion:

- Synchronous to the existing `pg_notify` publish on `request_completed`
  (new channel).
- Backed by a bounded write queue (10k entries, drop-oldest on overflow
  with a metric) so a Postgres hiccup doesn't stall the proxy.
- Sensitive headers (`Authorization`, `Cookie`, etc.) are never written;
  only the `request_id` correlation key.

### Query path

New endpoint:

```
GET /v1/request-logs?
  app=lsd           # filter by app slug (required)
  path=/api/users   # substring or glob
  status=4xx,5xx    # comma list of buckets
  consumer=<uuid>   # filter to a single consumer
  since=2026-08-01  # RFC 3339
  until=2026-08-18
  cursor=<opaque>
  limit=200         # max 1000
```

Response is a cursor-paginated array of `RequestLogSummary` (no bodies,
no headers, just metadata). Cursor uses `(started_at DESC, id DESC)`
keyset.

### Retention

- Hot: 7 days in `request_logs`.
- Cold (after 7 days): aggregated into `request_logs_hourly` with the
  same columns except `request_id` and `user_agent_hash`. 90 days.
- After 90 days: dropped. Aggregates only.

### Limits (pkg/api/limits.go)

- `request_logs_query_max_rows` = 1000.
- `request_logs_query_max_window_days` = 7 (against hot table).
- `request_logs_path_max_length` = 512.

## Acceptance

1. After #05, a customer can query
   `GET /v1/request-logs?app=foo&status=5xx&since=...` and see the
   rows.
2. Reconciliation: count of `request_logs` for `(app_id, day)` matches
   `sum(rate(gateway_requests_total{app_id}[1d]))` over the same window
   (property test).
3. Cold `request_logs_hourly` aggregate is materialized by a scheduled
   job (new CRON — uses the existing `meterd` cron framework).
4. No header values containing secrets are present in any test row
   (negative test against `Authorization`, `Cookie`, `Set-Cookie`).
5. Migration is numbered in the next available slot; cross-PR fence check
   runs before commit.

## Dependencies

- #05 (consumer_id column).
- Feeds #09 (replay reads from this table).

## Audit provenance

- `pkg/api/logs.go:12-23`, `:84-126` — current logs DTO is stdout/stderr only.
- `docs/adr/043-app-logs-stream.md:5-18` — ring buffer backing store.
- No `request_logs` table or `/v1/logs` request-envelope endpoint exists.
