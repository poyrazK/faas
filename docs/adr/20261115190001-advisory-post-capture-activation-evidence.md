# ADR-20261115190001 · Advisory post-capture activation evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [snapshot priming](20261115180001-advisory-snapshot-priming-evidence.md)
  and [observed history](20261115100001-observed-build-history.md).

Snapshot-priming evidence does not explain durable work after capture and before
deployment activation. Add optional `post_capture_health` for imaged targets in
explicit v2 profiles, reusing existing `imaged_post_capture_*` gauges. Keep v1
and unset profiles unchanged. Add no producers, database reads, probes, alerts,
eligibility decisions, remediation, workflow activation or deployment gates.
Activation handler-return counters remain outside this increment; a successful
return is not necessarily a new activation.

Each imaged replica independently observes the shared fleet, unlike scheduler
ownership scopes. Require explicit scope `replica_observations_of_shared_fleet`.
Retain each job/target observation and compare replica views without summing
their deployment counts or inferring consensus. Different fresh durable reads
can disagree legitimately; only malformed or duplicate source series produce
conflicting evidence. Aggregate counts describe replica views, never unique
deployments or successful activations.

Read a single closed selector and its original timestamps under the existing
pinned evaluation time and shared three-second API budget. Reuse exact up-target
joins, closed metric/class families, full-frame pairing, original clock checks,
finite values, duplicate detection and existing sample bounds. No up targets
adds no reads. Preserve expected missing targets and unknown compute inventory.

Separate collection status, observer expectation and eight ordered classes:
`publication_pending`, `activation_pending`, `dependency_held`,
`compatibility_held`, `hosting_verifying`, `hosting_retry`, `gate_overdue`,
`unknown`. Each usable class requires fresh successful collection after the
evidence boundary, a fresh successful scrape and class samples from the same
original scrape as collection metadata. Assess classes independently so positive
evidence survives a gap elsewhere. False observer expectation can coexist with
nonempty evidence and does not imply drained work. Fresh collection metadata
can report failure, no successful read yet, or expired last-success timing;
none establishes an empty fleet view.

Retain per-view `pending` and `unknown_age` counts from durable evidence.
Unknown ages are included in pending; the unknown class requires all ages to be
unknown. `oldest_boundary_at` and `oldest_known_age_seconds` exist only when some
timing is known. Empty or entirely unknown ages omit both fields rather than
fabricating zero wait. Boundaries can be capture park time, init publication or
retained gate/verification evidence. They can precede the rollout and are not
continuous residence in a class, execution duration or new stall thresholds.
Retain existing clock sanity/skew rules and the centralized exact-integer wire
ceiling in `pkg/api/limits.go`, not an admission quota.

Hold and overdue classes retain reported dependency, compatibility or hosting
verification evidence. They do not independently establish present eligibility,
continuous blocking, deployment readiness or a new health verdict. Use nonempty
for any positive class evidence, including unknown. Known empty requires all
eight classes observed empty. Complete component evidence can include unknown
historical ages; keep those counts explicit.

Aggregate all inspected replicas before the 32-item preview. Prioritize nonempty
views by oldest known boundary age, then incomplete and empty observations.
Post-capture detail yields first under the existing 64 KiB API and 1 MiB report
bounds, preserving aggregate view counts and explicit omissions. Independently
decode the optional section, including explicit scope, false and zero fields,
so malformed evidence preserves other diagnostics. Artifacts omit raw labels,
endpoints and provider errors; Markdown exposes only aggregate view evidence
and observation history, not per-target or customer identities.

Retain state, class-view counts and coverage changes in the shared 16-event,
16 KiB history; routine clocks, ages and pending magnitudes do not create events.
Progress keeps first/last nonempty and complete empty observations, sampled
empty-to-nonempty transitions, throughout status and sticky gaps/unknown ages.
Gaps cannot fabricate emptiness or erase previous observations. Disappearing
evidence can reflect activation, cancellation, retained-evidence changes or
collection scope, so it does not prove recovery or completion of the same work.
Keep deployment health qualification, exits and poll scheduling unchanged.

Verify all eight classes, gate evidence remaining advisory, replica divergence
without false conflicts, known/mixed/unknown ages, collection freshness/failure,
observer shutdown with nonempty evidence, partial positives, exact counts,
malformed scope/jobs/classes/frames/clocks, inventory gaps, preview/byte bounds,
history clipping and ownership, optional JSON isolation, protected API access,
unchanged qualification and SDK round trips.
