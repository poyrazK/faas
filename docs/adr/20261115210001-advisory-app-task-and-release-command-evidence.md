# ADR-20261115210001 · Advisory app-task and release-command evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [task observation](20261017141024523-app-task-operator-observation.md)
  and [observed history](20261115100001-observed-build-history.md).

Daemon health and snapshot priming's `release_blocked` class do not explain
outstanding release commands or their dispatcher. Expose optional
`app_task_health` for scheduler jobs in explicit v2 profiles: `schedd` for
single-box and both `schedd` and `schedd-compute` for control-plane. Reuse
`schedd_app_task_*` gauges; add no database reads, producers, execution changes,
alerts, remediation or deployment gates. Preserve v1 and unset profiles.

Keep three kinds, `release`, `manual`, `cron`, and nine classes in fixed order:
`queued`, `retry_wait`, `held`, `deadline_overdue`, `restoring`, `running`,
`cancellation_pending`, `lease_expired`, `unknown`. Validate the complete closed
27-pair family and preserve kind/class identity. Use explicit scope
`replica_observations_of_shared_fleet`. Each scheduler observes shared fleet
tasks, regardless of its local handlers. Compare replica views without summing
tasks, inferring consensus or subtracting local slot occupancy from fleet work.

Reuse the strict bounded gauge reader, extended to two fixed dimensions, with
exact up-target joins, finite values, duplicate and orphan detection, and paired
original sample frames. Read a closed selector and its timestamps per selected
job, at the same pinned time and within the existing three-second API budget.
No up targets adds no task reads. Retain expected missing targets and unknown
compute inventory.

Separate collection status, observer expectation, local dispatcher and each
durable kind/class. Durable values require a fresh successful read after the
evidence boundary, fresh successful scraping and original clocks matching the
collection sample. Failed, never successful, stale or pre-boundary collection
cannot establish empty work. Partial positive evidence survives sibling gaps.
Observer expectation, dispatch bits, slots and activity clocks are independently
usable even when durable collection fails.

Retain `pending` and its included `unknown_age` subset. Known boundaries are
queued admission timestamps, running start, cancellation request, expired lease
or missed start deadline. They may precede the rollout and are not continuous
class residence, execution deadlines or new stall thresholds. Restoring,
retry-wait, held and unknown classes require all ages unknown. Empty or entirely
unknown timing omits both oldest boundary and known-age fields. Validate exact
integer wire bounds centrally in `pkg/api/limits.go`.

Dispatcher evidence retains explicit configured dispatch and runner-running
bits, configured/in-use local handler slots, and optional last successful claim
and sweep clocks. Derive only `disabled`, `running`, `stopped`: stopped means
enabled but inactive; running does not establish progress. Slots include restore,
execution and teardown, not VM or fleet-task counts. Claim success includes empty
results; busy handlers need not claim. Sweep success includes empty sweeps. These
are not task completion or delivery acknowledgements. Introduce no progress
deadline, failure attribution or new health verdict.

Aggregate replica coverage, kind/class view counts, observer and dispatcher
activity before the 32-item preview. Prioritize nonempty views by oldest known
boundary, then incomplete and empty views. New detail yields first under the
shared 64 KiB API and 1 MiB report caps. Preserve aggregates and explicit
omissions. Align inherited preview schema bounds with the implemented 32-item
limit. Decode the optional section independently, requiring kind, scope and
explicit false/zero fields, so malformed task evidence preserves other sections.
Expose no commands, output, task/app identities, raw endpoints or provider errors.
Markdown retains aggregate replica and local-activity view counts only.

Retain state, kind/class and dispatcher-view coverage in the shared 16-event,
16 KiB history. Routine clocks, ages, pending magnitudes and occupied slot
magnitudes do not create events when activity categories are unchanged.
Progress retains first/last nonempty and complete empty observations, sampled
empty-to-nonempty transitions, empty-throughout status and sticky gaps/unknown
ages. Clipping cannot erase previous progress. Disappearing tasks do not prove
successful execution, recovery or deployment readiness. Preserve qualification,
exit behavior and polling.

Verify all 27 pairs, unknown-age-only classes, stale/failed collection with
independent dispatch, disabled/stopped/saturated workers, malformed pairs and
frames, partial positives, inventory gaps, replica overlap, preview/report/history
limits and ownership, optional JSON isolation, protected API access, unchanged
qualification, and SDK round trips.
