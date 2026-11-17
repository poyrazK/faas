# ADR-20261116120000 · Observed command-cron outcomes

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-16
- **Extends:** [cron admission evidence](20261116080000-advisory-command-cron-admission-evidence.md),
  [sampled operation counters](20261116090001-observed-app-task-operation-outcomes.md)
  and [history compaction](20261116110000-bounded-runtime-history-compaction.md).

A due schedule and a downstream task result do not explain the coordinator's
intervening decisions. Add optional advisory `command_cron_outcomes` for explicit
v2 scheduler jobs. Single-box observes schedd; control-plane also observes
schedd-compute. Preserve v1 and unset profiles. Use existing counters without
producer, database, scheduler, admission, retry, alert or health-gate changes.

Inspect three fixed families and 29 ordered signals. `schedd_command_cron_sweeps_total`
has success, error, canceled. `schedd_command_cron_decisions_total` has disabled,
not_due, account_held, plan_limit, replacement_wait, not_owned, invalid_schedule,
lookup_error, store_unavailable, no_live_deployment, canceled, legacy_queued,
legacy_skipped, legacy_stale, legacy_error. `schedd_command_cron_admissions_total`
has queued, exclusive_pending, coalesced, skipped_overlap, missed_deadline, stale,
deployment_unavailable, rejected, error, canceled, unknown.

Keep each returned classification and source separate. A shared successful scan
includes inline HTTP work and does not prove command admission. Decisions and
legacy calls are not unique scheduled occurrences. Queued, pending or coalesced
admission does not prove execution or delivery. Rejections, cancellation, overlap
policy and stale compare-and-swap results are not automatically infrastructure
faults. The admission outcome unknown is an actual returned classification;
missing telemetry remains independently unknown. Do not sum the three families
as unique work, infer admission latency from counters, or label commands,
arguments, cron/occurrence/task/app identities, output, tokens or provider errors.

Use scope `process_local_counter_observations`. Reuse the existing exact-integer
counter ceiling, pinned current/comparison times, prior scheduler up/identity/
uptime reads, freshness and boundary checks, matching original scrape clocks and
one-second uptime advance tolerance. First samples are baselines. Changes in
process identity, new/repeated samples, uncertain windows, counter decreases,
missing/stale points and source failures establish no delta. Comparable points
retain exact differences including zero, not rates or exact event timestamps.
Sampled continuity cannot exclude every intervening restart. Assess signals
independently so partial positive evidence survives sibling gaps.

Read the closed metric selector and original timestamps at each evaluation time,
with bounded frames, exact up-target joins and duplicate/orphan checks. All reads
share the existing three-second API budget. Existing loop-health reads have
priority; new counter reads start after those reads finish and use the remaining
deadline. The verifier retains three protected
HTTP reads per poll without extra polls. Preserve authorization, MFA, qualification,
exit behavior and stability logic. Independently reject malformed optional data
without discarding valid core health or sibling diagnostics.

Aggregate full-set signal coverage and per-source/outcome categories before the
32-target preview. Counts describe signals, not delta magnitudes. Preview items
yield first under the existing 64 KiB API and 1 MiB artifact caps, with explicit
omissions. History includes the new summary in its closed lossless codec catalog
under unchanged 16-event/16 KiB and decoded 64 KiB bounds. Signatures omit clocks,
point magnitudes and deltas. Sticky progress retains first/last increases, a
canonical bounded union of observed source/outcome categories, baselines and
incomplete evidence. Never sum overlapping poll deltas or infer successful
execution. Existing complete historical proofs still validate after compaction;
explicit detail omissions are not source gaps.

Verify every producer outcome, baselines, zero/exact deltas, resets and process
uncertainty, missing/stale/invalid frames, partial positives, scope, full-set counts
before clipping, optional wire isolation, protected transport without new polls,
privacy, sticky category history and ownership, report limits, combined-profile
compaction and SDK round trips.
