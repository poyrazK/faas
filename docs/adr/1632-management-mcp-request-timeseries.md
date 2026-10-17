# ADR-1632: Historical hourly request trends through management MCP

## Status

Accepted. Extends ADR-1631 using the existing native request analytics timeseries
API and typed client. No native endpoint, state, schema or migration change.

## Decision

Add read-only `gregale_get_request_analytics_timeseries`. Reuse per-app diagnostic
consent across environments, native ownership/plan/retention checks and final
immutable app identity checks. Read the existing native timeseries endpoint
once without group_by; reject unexpected grouping/series or bucket width.
Do not expose consumer/dimension identities or fetch request examples.

Accept the same bounded duration/day or absolute since and historical exclusive
until as the overview, with native 24h default and retention cap. Share window
validation rather than manufacturing grouping metadata. Preserve actual native
since/from/until/as_of and window_clamped; reject widened windows or changed
historical ends without fallback. Source completeness and freshness stay unknown.

Select app-wide data by omitting both route and method, or an exact unchanged
pair by supplying both. Preserve native method-prefixed labels such as
GET /orders; never strip prefixes or reconstruct modified labels. Bound selectors
to 256 bytes and reject query/fragment, control, invalid UTF-8 and credential
values before the analytics read. Normalize supported HTTP methods to uppercase
as the native API does. Add timeseries_filter_usable to overview rows, distinct
from discovery's route_filter_usable, only for unchanged supported exact pairs.
Expose diagnostic scope app_wide separately from series_scope app_wide or
route_method; this read adds no permissions or environment narrowing guarantee.

Return explicit point projections: counts, HTTP 4xx/5xx error rate, cold boots
and count-weighted latency percentiles. Reuse observed-zero/absent latency
semantics. A native zero-filled bucket has no retained requests and omits
percentiles, never proof of zero incoming traffic, capture completeness or health.
The native timeseries has no guest/resource measurements. Never average or sum
bucket percentiles into a window percentile, infer statistical significance or
claim deployment causality.

Validate the full native ordered contiguous UTC hourly grid and exposed metrics,
including points omitted by the output prefix. PostgreSQL generates hours from
floor(from) through floor(until - 1 microsecond). Honor that microsecond-exclusive
boundary instead of inventing a nanosecond bucket. Missing, extra, duplicate,
unsorted or non-hour points fail the read. Valid sub-microsecond windows can have
no returned buckets, distinct from a zero-filled grid. Annotate every returned
hour with nominal start/end, clipped window_from/window_until and partial flags.
Counts in partial hours must not be presented as full-hour volume or rates.

Centralize output limits in pkg/api/limits.go: default 48 and maximum 168 points.
Preserve the oldest-first native prefix without forwarding a query limit,
changing the selected window or silently fetching additional ranges. Native and
returned point counts plus points_truncated remain explicit. Top-level status
reflects all native points, including omitted buckets; an empty prefix must not
hide retained evidence in omitted points. No local window totals/percentiles
are manufactured from the prefix. Overview/trends/discovery are independent
reads, not an atomic snapshot, and discovery lacks method/historical-until
filters. Fetching the performance recipe itself retrieves no evidence.

## Consequences

Agents can locate retained performance changes in time with explicit selection,
precision and sampling limits. There are 40 read/planning tools, 48 with writes
enabled and six prompts. Protocol and pinned real Codex fixtures cover app-wide
and exact-pair selection, consent, empty/partial buckets, bounded output,
restart/refresh and zero model requests/native writes. Portable fixture
acceptance does not qualify a native/provider deployment.
