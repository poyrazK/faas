# ADR-20261115170001 · Advisory image-preparation phase evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [handoff evidence](20261115160001-advisory-build-image-handoff-evidence.md)
  and [observed history](20261115100001-observed-build-history.md).

Build handoff evidence stops at the creation of a preparation checkpoint. Expose
optional `preparation_health` for imaged members of explicit v2 profiles using
the existing preparation progress gauges. Do not add database reads, producers,
probes, alerts, remediation, workflow activation, or deployment gates. Keep v1
and unset profiles unchanged. Downstream capture and activation remain separate.

Reuse the strict batched gauge reader with a closed phase dimension. Read one
fixed selector and its original timestamps, preserving metric names before
`timestamp` removes them. Require exact up-target joining, full series-frame
pairing, finite values, duplicate detection, closed families/phases and existing
sample bounds. The preparation reader runs beside other readers under the same
evaluation time and shared three-second request budget; no targets adds no reads.

Separate collection status, observer expectation, and three ordered durable
phases: `preparing`, `layer_published`, `scan_complete`. Require a fresh successful
scrape and original sample clocks. A usable phase requires fresh successful
collection after the evidence boundary and the same raw scrape clock as its
collection metadata. Validate each phase independently so usable pending work
survives missing or invalid evidence in another phase. A fresh false observer
expectation can coexist with pending checkpoints and does not imply drained work.

Counts represent active and recoverable checkpoints within one recovery scope.
Legacy empty owners and compatibility readers may overlap replica scopes, so
never sum pending values into a fleet or unique-deployment total. Summary counts
describe waiting, empty and unknown-age observation scopes, including separate
scope counts for each phase. A known empty scope requires all three phases to
be observed empty; no targets stays unknown. Full component availability does
not mean all historical ages are known. Coverage gaps stay explicit alongside
positive pending evidence.

`unknown_age` is included in `pending` and cannot exceed it. Only pending rows
with known phase timing contribute to `oldest_phase_started_at` and
`oldest_known_age_seconds`. Both timing fields are absent when all ages are
unknown or the phase is empty; never fabricate a zero age for legacy rows.
Durable phase age includes time waiting to resume and can precede the rollout;
it is not execution duration or proof of a stall. Retain the existing ten-year
clock sanity bound and skew allowances. Centralize the JavaScript-safe exact
integer telemetry ceiling in `pkg/api/limits.go`; it is not an admission quota.

Aggregate before the 32-item preview. Prioritize pending scopes, oldest known
age first, then incomplete and empty observations. Preparation detail yields
first under the existing 64 KiB API and 1 MiB report bounds with explicit
omissions and retained counts. Decode the optional section independently,
including required false and zero fields, so malformed preparation JSON cannot
discard other evidence. Artifacts exclude raw labels, endpoints and errors;
Markdown summaries expose only aggregate scope counts and observation history.

Retain state, phase-scope counts and coverage changes in the shared 16-event,
16 KiB history. Ages, clocks and pending magnitudes do not create events. Keep
first/last waiting and complete empty observations, sampled empty-to-waiting
transitions, throughout status, sticky coverage gaps and sticky unknown ages.
Gaps cannot become empty or erase waiting evidence. Disappearing checkpoints
can reflect handoff, cancellation or scope changes; never infer completed image
preparation or successful deployment from these sampled observations.

Verify all profiles, empty versus unknown, failed/expired collection, observer
shutdown with pending work, known/mixed/unknown ages, partial positives, exact
large counts, invalid frames/clocks/phases, overlapping scopes, preview and byte
limits, history clipping and ownership, optional JSON isolation, protected API
access, unchanged polling/qualification and SDK false/zero round trips.
