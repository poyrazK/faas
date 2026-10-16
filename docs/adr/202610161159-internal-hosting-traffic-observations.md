# ADR-202610161159 · Internal hosting traffic observations

- **Status:** accepted for a default-off internal pilot; provider qualification pending.
- **Date:** 2026-10-16
- **Amends:** [static hosting](20261015-internal-static-hosting.md) and [storage accounting](202610161126-hosting-account-storage-reservations.md).

## Decision

Use a dedicated Cloudflare Analytics Engine dataset for best-effort per-site
observations. A separately gated Worker binding enqueues one data point after
an authenticated response is selected, without a telemetry HTTP fetch, Gregale
RPC, response-body buffering, acknowledgement, or `waitUntil` dependency. Missing
bindings or synchronous collection errors leave static responses unchanged.
Cloudflare's runtime handles writes asynchronously and may drop or sample them.

Record the site UUID as the sole sampling index; origin fingerprint, release UUID,
closed result code, file-cache outcome and schema version as blobs; and request
count, expected response payload bytes and status as numbers. Do not collect IPs,
email, tokens, paths, hostnames, query strings, headers or bodies. Custom aliases
use their validated canonical site UUID. Attribute only after Access verification.
Pre-Worker traffic, access denials and custom-binding failures before verification
remain outside this view. Record expected file size only for successful GETs;
HEAD and 304 have zero payload, and delivery/cancellation is not measured.

## Queries and dashboard

A separately gated operator/MFA/read-scope API verifies account/site/origin ownership
before querying Cloudflare. Use a separate account-scoped Analytics read token.
The fixed 24-hour SQL template permits only validated immutable dataset names,
site UUIDs, origin digests and server-generated UTC timestamps. Analytics Engine
has no parameter API; this external provider query does not run in PostgreSQL or
use its sqlc layer. No user-supplied SQL, filters, windows or raw events are exposed.

Weight aggregate counts and payload by `_sample_interval`. Reject malformed,
truncated, oversized, overflowing or unknown-schema results. Query concurrency is
two per apid with a five-second HTTP timeout. Coalesce same-site reads and keep a
one-minute cache capped at 128 reports, keyed by site and origin. Every API request
rechecks flags and ownership even when the provider result is cached. Responses
are `no-store`; data is copied before returning it from the internal cache.

The independently gated operations panel shows approximate counters, hourly
trends and the 20 most recent error groups, not individual request logs. Provider
failure is unavailable, not zero traffic; empty observations require checking
collection. File-cache hits exclude HEAD/304 and metadata/browser cache activity.
Expected payload is not bytes received by visitors. CPU and provider operations
are not inferred, and existing cost calculators remain hypothetical.

## Operations

Infrastructure binding, Worker collection, API reads and dashboard build all
require explicit opt-in; every default stays off. Disabling collection stops new
observations without deleting the dataset. Disabling API reads hides stored data
without stopping collection. No database migration, new daemon, customer billing
or provider subscription change is introduced.

Cloudflare currently documents three-month Analytics Engine retention. Dataset
creation occurs on the first write, so a prepared but unwritten dataset can return
an unavailable query rather than an empty result. Operators must review provider
availability, quotas, pricing and retention before enabling the pilot. Existing
observations follow provider retention after flags are disabled; no per-site
provider deletion or configurable shorter retention is claimed.

References: [enqueue semantics and dataset creation](https://developers.cloudflare.com/analytics/analytics-engine/get-started/),
[sampling](https://developers.cloudflare.com/analytics/analytics-engine/sampling/),
[SQL API](https://developers.cloudflare.com/analytics/analytics-engine/sql-api/),
[limits and retention](https://developers.cloudflare.com/analytics/analytics-engine/limits/),
[pricing](https://developers.cloudflare.com/analytics/analytics-engine/pricing/).
