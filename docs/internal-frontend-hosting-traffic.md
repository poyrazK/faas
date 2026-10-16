# Internal hosting traffic observations

This extension remains internal and default-off. It adds provider-side observations
and a read-only operations panel, without changing routing, customer entitlements
or bills. See [ADR-202610161159](adr/202610161159-internal-hosting-traffic-observations.md).

## Prepare and enable

1. Use the existing isolated hosting pilot. Review Analytics Engine account
   availability, [pricing](https://developers.cloudflare.com/analytics/analytics-engine/pricing/),
   quotas and [retention](https://developers.cloudflare.com/analytics/analytics-engine/limits/).
   The provider currently documents three-month retention, not a configurable
   per-site TTL. Disabling flags does not erase retained observations. Current
   pricing documentation describes Free/Paid allowances and future usage rates;
   verify actual account billing rather than assuming telemetry stays free.
2. Set Terraform `configure_analytics = true`, leaving `analytics_enabled = false`.
   This attaches the dedicated `ANALYTICS` binding to
   `gregale_internal_<pilot-name>_traffic`. The dataset is created by the provider
   on its first write, not during Terraform apply. Binding setup does not enable
   Worker serving, routing, collection or API management.
3. Inject a **separate sealed** `FAAS_FRONTEND_HOSTING_ANALYTICS_CF_TOKEN` with
   Analytics read permission on the configured Cloudflare account. Set
   `FAAS_FRONTEND_HOSTING_ANALYTICS_DATASET` from Terraform's `apid_environment`.
   The existing `FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID` must identify the same
   account. No token appears in Terraform outputs, API reports, or the Worker.
4. Explicitly opt into collection with Terraform `analytics_enabled = true`.
   The Worker also requires its existing main serving switch and Access/routing
   configuration. Only authorized attributable requests enqueue observations.
5. After every apid is upgraded/configured, enable the independent
   `frontend_hosting_analytics_enabled` runtime flag. Its bootstrap environment
   setting is `FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED=1`; Terraform leaves it `0`.
   Main hosting management, MFA, operator authorization, read scope and account
   ownership still apply. No database migration is required for this milestone.
6. Build the operations console with the existing main hosting build flag and
   `NEXT_PUBLIC_FRONTEND_HOSTING_ANALYTICS_ENABLED=true`. The per-site panel also
   requires the backend's `analytics_enabled` capability. Other hosting controls
   continue working when analytics is disabled or unavailable.

An unwritten dataset can return a provider query failure. Send authorized pilot
traffic first, allow provider ingestion time, then refresh. Do not reinterpret
provider errors as no traffic or silently fall back to fabricated zero counters.

## What the observations mean

The sampling index is the canonical site UUID. Blob positions are origin digest,
release UUID (or empty), fixed response code, file-cache outcome and `v1`. Numeric
positions are one request, expected payload bytes, and HTTP status. No IP, email,
JWT, hostname, path, URL, query, header or response body is stored. Custom-domain
requests use the verified alias's canonical site UUID, sharing the site's counts.

The Worker calls the provider's nonblocking `writeDataPoint` binding once after
selecting an authorized response. There is no telemetry network call to Gregale,
no response stream cloning/buffering for metrics, and no acknowledgement wait.
A missing/broken binding does not change response bytes, status, auth or caches.
Async provider drops/quota exhaustion cannot be detected per response.

Coverage deliberately excludes Access denials, traffic blocked/redirected before
this Worker, and custom-binding failures before identity verification. Canonical
requests are attributed after Access verification, including subsequent routing
failures. Unknown site UUIDs are never readable through the ownership-checked API.
The view is not total Internet traffic, a billing ledger or an uptime probe.

Expected payload uses the declared immutable file size for a successful GET,
including cache hits. It does not detect a disconnected visitor or measure wire
compression/headers/delivery. HEAD and 304 have zero payload and no file-cache
lookup. Hit/miss counts cover the Worker file cache, excluding manifest/routing
metadata caches and browser caches. There is no inferred R2-operation or CPU meter.
Existing provider cost estimates remain separate; analytics write/query charges
are outside the R2/Worker usage calculator.

## Read and interpret

`GET /v1/internal/hosting/sites/{site}/traffic` returns a fixed trailing 24-hour
window ending at the last completed UTC minute. No arbitrary query, account,
hostname or time-window input is accepted. Queries include the owned site's UUID,
its pinned origin digest and schema `v1` in a validated dedicated dataset.

The report contains approximate totals, hourly buckets, up to 20 recent failure
groups, maximum provider sample weight, the queried window and observation time.
Weight counts and payload using `_sample_interval`; those results still have
sampling error, ingestion lag and best-effort collection gaps. Hour buckets at
both ends may be partial. Empty hourly buckets are displayed as zero observations,
not proof of zero served requests. Error groups are hour/status/code aggregates,
not individual event timestamps, paths or logs.

Provider query concurrency is bounded to two per apid and HTTP time to five
seconds. Same-site/origin queries coalesce. A one-minute cache holds at most 128
reports per process. Refresh may reuse that cache. Ownership and flags are checked
before every cache read. Responses use `Cache-Control: no-store`. Provider errors,
oversized/malformed responses, excessive rows and numeric overflow produce the
bounded `hosting_traffic_unavailable` error without returning raw provider data.
Independent read-gate errors are `frontend_hosting_analytics_disabled` and
`frontend_hosting_analytics_unconfigured`.

Turning off Worker collection preserves serving and stored observations. Turning
off API or dashboard flags hides reporting while collection may continue. Keep
that distinction in rollout and incident procedures. To stop both, disable both.
Dataset lifecycle and retention remain provider-owned; this pilot does not claim
per-site erasure or shorter retention.

## Qualification

Portable checks exercise independent flags, Access and alias attribution, cache
hits, HEAD/304 payload semantics, telemetry failures preserving serving, weighted
aggregation, ownership-before-provider checks, query bounds, cache immutability,
malformed/oversized provider responses and UI unavailable/empty states.

Before live use, opt into a disposable isolated pilot, compare known authorized
GET/cache/HEAD/304/error traffic with delayed weighted observations, verify direct
provider query scope and token permissions, and check that unauthorized visits
create no site observations. Stop the query provider/disable its token and confirm
static serving and deployment controls continue. Disable collection and verify
serving remains intact while new observations cease after provider ingestion lag.
Inspect retained dataset fields for the documented privacy contract. Verify real
sampling/retention behavior and account charges separately. Local fixtures do not
establish any of those live properties.
