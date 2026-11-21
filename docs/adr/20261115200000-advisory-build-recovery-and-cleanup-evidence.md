# ADR-20261115200000 · Advisory build recovery and cleanup evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [post-capture evidence](20261115190001-advisory-post-capture-activation-evidence.md)
  and [observed history](20261115100001-observed-build-history.md).

Builder execution and image activation evidence do not show durable running
build claims or outstanding builder VM cleanup obligations. Add optional
`build_recovery_health` for apid in explicit v2 monitoring profiles. Reuse the
existing seven build-recovery gauges and their collection status; add no metric
producer, database read, probe, remediation, alert or deployment gate. V1 and
unset profiles retain their existing behavior.

Each apid replica reads the shared fleet. Require scope
`replica_observations_of_shared_fleet` and retain each target view independently.
Aggregate counts describe replica coverage and nonempty/empty views; never sum
running claims or cleanup obligations into a fleet total. Different fresh reads
may disagree legitimately. Only malformed or duplicate source evidence is
conflicting.

Use a closed selector for `apid_build_recovery_collection_success`,
`apid_build_recovery_collection_last_success_timestamp_seconds`,
`faas_build_running_count`,
`faas_build_running_oldest_started_timestamp_seconds`,
`faas_build_vm_cleanup_pending`,
`faas_build_vm_cleanup_oldest_created_timestamp_seconds`, and
`faas_build_vm_cleanup_retry_pending`. Read it and original timestamps at the
existing pinned evaluation time, inside the shared three-second budget. Reuse
exact up-target joins, bounded samples, finite values, freshness, boundary and
clock checks. No up targets adds no recovery reads.

Separate collection, running claims and cleanup obligations. Usable work values
require a fresh successful collection after the evidence boundary, fresh
successful scraping, and component samples coherent with the collection's
original scrape clock. Assess running and cleanup independently: positive
evidence survives a missing or invalid sibling. Failed, never successful, stale,
pre-boundary or missing collections cannot establish empty work. There is no
observer-enabled or unknown-age gauge in this source; do not invent either.

Running evidence retains `count`, `oldest_started_at` and `oldest_age_seconds`.
Cleanup retains `pending`, `retried`, `oldest_created_at` and
`oldest_age_seconds`. Retried is a subset of outstanding obligations with a
recorded failure, not retry attempts or a rate. Pending includes claimed or
retrying obligations. Cleanup age begins at obligation creation and survives
retries; it is not time since the last attempt. Positive counts require valid
origins and matching ages; empty counts omit both timing fields. Retain the
central exact-integer wire bound in `pkg/api/limits.go` and existing clock sanity
rules. These are validation bounds, not admission limits or stall thresholds.

Running claims do not prove active VMs. Cleanup obligations do not prove live
or leaked VMs. Old origins may precede the rollout. No new execution deadline,
failure threshold, liveness conclusion or eligibility decision follows from
these observations.

Aggregate before the 32-item preview; prioritize cleanup, then running, then
incomplete and empty views, oldest evidence first. Recovery detail yields first
under the shared 64 KiB API and 1 MiB report limits. Preserve aggregate view
counts, explicit omissions and other evidence. Decode the optional section
independently, requiring explicit scope and required false/zero fields. Keep raw
endpoints and provider errors out of artifacts; Markdown reports aggregate
views and observation history without per-target identifiers.

Retain state, component-view counts and coverage changes in the shared 16-event,
16 KiB history. Routine clocks, ages and work magnitudes do not create events.
Progress records first/last nonempty and complete empty observations, sampled
empty-to-nonempty transitions, empty-throughout status and sticky gaps. Work
disappearing between polls does not establish successful completion or recovery
of the same claims or obligations. Qualification, exits and polling are unchanged.

Verify empty/running/cleanup/both and partial positives, retry subsets and
creation origins, collection failures and freshness, invalid values and clocks,
replica coverage without fleet totals, preview and byte limits, bounded history
and ownership, optional JSON isolation, protected API access and SDK round trips.
