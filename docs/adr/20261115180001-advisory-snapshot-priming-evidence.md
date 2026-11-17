# ADR-20261115180001 · Advisory snapshot-priming evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [preparation evidence](20261115170001-advisory-image-preparation-phase-evidence.md)
  and [observed history](20261115100001-observed-build-history.md).

Image-preparation evidence ends before scheduler-owned snapshot priming. Add
optional `snapshot_prime_health` using existing `schedd_snapshot_prime_*` gauges.
Select `schedd` in single-box-v2 and both `schedd` and `schedd-compute` in
control-plane-v2. Keep v1 and unset profiles unchanged. Do not add producers,
database reads, probes, alerts, remediation, eligibility decisions, deployment
gates or operational activation. Post-capture activation remains separate.

Read one closed selector and its original timestamps per selected scheduler
job, under the same pinned evaluation time and shared three-second request
budget. Reuse strict full-frame pairing, exact up-target joins, finite values,
closed class labels, duplicate detection and existing per-job sample bounds.
No up targets adds no metric reads. Keep job and sanitized target identity on
each observation. Expected counts include all selected scheduler jobs; preserve
missing configured compute targets and unknown inventory separately.

Separate collection status, observer expectation and six ordered evidence
classes: `waiting`, `release_blocked`, `active`, `publication_pending`,
`snapshot_recorded`, `unknown`. Class values require fresh successful collection
after the evidence boundary, a fresh successful scrape and coherent original
sample clocks matching collection metadata. Assess classes independently so
usable positive evidence survives a gap in another class or scheduler job.
Observer shutdown can coexist with nonempty evidence and does not imply drained
work. Available metadata can still report failed or expired collection.

Each class retains the producer's `pending` and `unknown_age` counts. Pending
means current attempts in that class, including active and recorded snapshots;
it is not exclusively idle or unfinished work. `unknown_age` is included in
pending and cannot exceed it. The `unknown` class requires every counted age to
be unknown. Only known ages contribute to `oldest_attempt_at` and
`oldest_known_age_seconds`; both are absent when no known ages exist, including
an empty class. Never turn absent historical timing into zero wait.

The attempt boundary accounts for stage start, release completion, runtime
invalidation and known instance activity. It can precede the rollout and move
forward as evidence changes; it is not continuous idle time, execution duration
or proof of a stall. Retain existing clock sanity/skew rules and centralize the
exact-integer telemetry ceiling in `pkg/api/limits.go`, not an admission quota.

Ownership scopes can overlap through empty-owner compatibility. Never sum
attempt counts across scheduler jobs or replicas into a fleet total. Aggregate
nonempty/empty/unknown-age observation scopes and per-class scope counts over
all inspected targets before preview clipping. Use `nonempty` for positive
attempt evidence, including `snapshot_recorded`, without implying a blocked
scheduler or ready deployment. Known empty requires all six classes observed
empty; source gaps remain explicit. Complete component availability can still
contain historical unknown ages.

Prioritize nonempty scopes by oldest known boundary age, then incomplete and
empty observations. Snapshot-prime detail yields first under the existing
32-item, 64 KiB API and 1 MiB report limits, retaining aggregates and explicit
omissions. Decode the optional section independently, requiring explicit false
and zero fields, so invalid snapshot evidence preserves all other evidence.
Private artifacts exclude raw labels, endpoints and errors; Markdown exposes
aggregate observations, not per-target or customer identities.

Retain state, class-scope and coverage changes in the shared 16-event/16 KiB
history. Routine ages, original clocks and pending magnitudes do not add events.
Progress retains first/last nonempty and complete empty observations, sampled
empty-to-nonempty transitions, throughout status and sticky gaps/unknown ages.
Gaps cannot fabricate emptiness. A recorded snapshot or disappearance from the
view does not establish successful deployment, recovery or completion of the
same attempts. Existing health qualification, polling and exit behavior remain.

Verify both v2 job sets, frozen v1 behavior, all six classes, known/mixed/unknown
ages, collection freshness/failure, shutdown with nonempty work, partial
positives, exact counts, malformed frames/clocks/classes/jobs, inventory gaps,
preview and byte limits, history clipping/ownership, optional JSON isolation,
protected API access, unchanged qualification and SDK round trips.
