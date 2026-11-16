# ADR-20261112202000: Pipeline retention, read availability and selected discovery

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-12
- **Amends:** ADR-1591

## Context

Source checks accept finite retention settings below 1 GiB, but the existing
warning uses 80% of 1 GiB for every source. Its two-minute debounce can exceed
the remaining WAL budget. Read gates safely deny rows through RLS, but an empty
API response can be mistaken for a successfully read empty dataset. Bounded
discovery makes supported tables after the first 128 public relations inaccessible.

## Decision

Persist source-specific retention evidence in a bounded, additive catalog JSON
field. Clamp remaining headroom to the source retention setting, PostgreSQL slot
safe WAL size, and the existing Gregale ceiling. Estimate remaining time only
from advancing cluster WAL across recent samples. Keep sampling cursors private,
omit stale/generation-mismatched evidence, and clear it on resync. Warn at 20%
headroom or a five-minute estimate. At 10% headroom or a two-minute estimate,
enqueue the existing retention incident without the ordinary debounce. Preserve
staging admission, finite retention, and explicit recovery; estimates never
authorize destructive actions. Older workers retain the existing fallback.

The private worker installs protected availability functions at the destination.
Upgraded read-only Data API runtimes discover them through the serving login and
compose their pre-request hook with existing deadline and refresh hooks, without
changing those hooks' pinned bodies. Availability checks use the authoritative
reader receipt and the requested table's RLS policy. A shared table lock fences
policy changes through the subsequent query. Authorized reads of a gated table
raise HTTP 503 `mirror_unavailable`, including empty tables and unmatched filters.
Unlisted readers retain RLS behavior; healthy tables remain readable during a
table-specific gate. Serving roles gain no private table access. Existing apps
must deploy the upgraded runtime; direct SQL still uses RLS.

Connection checks accept either a bounded exact table selection or a literal
name search. Apply searches before the discovery limit; inspect exact names with
the existing compatibility inspector. Save the selection in the existing check
JSON through claims and completion. Coalesce only matching selections; replacing
one fences the old lease. Creation and saved setup request checks for their exact
tables. The metadata/selection limits and atomic evidence admission remain intact.

## Validation and scope

Regression checks cover discovery beyond the default page, invalid selections,
request coalescing and stale workers, source-specific headroom, sampled estimates,
urgent incident persistence, and empty/unauthorized/healthy-table reads. Native
replication acceptance exercises the upgraded API contract through schema and
table recovery. This preserves operator qualification and production gates.
