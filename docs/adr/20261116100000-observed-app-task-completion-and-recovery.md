# ADR-20261116100000 · Observed app-task completion and recovery

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-16
- **Extends:** [operation outcomes](20261116090001-observed-app-task-operation-outcomes.md).

Operation success is not a task verdict. Add optional advisory `app_task_results`
for explicit v2 scheduler jobs using existing process-local counters. Preserve v1
and unset profiles. Single-box observes schedd; control-plane also observes
schedd-compute. No producer, database, execution, retry, health gate, alert or
operational activation changes.

Inspect 18 fixed source/kind/outcome dimensions. Completion uses
`schedd_app_task_results_total`: release, manual and cron, each with succeeded,
failed, timed_out, cancelled, queued. Use the successfully acknowledged returned
row; queued represents policy retry. Failed results can include both customer
verdicts and platform failures. Recovery uses `schedd_app_task_recovery_total`:
requeued_restores, failed_runs, cancelled, with no task kind. These are existing
successful sweep result fields, not inferred retries. Expired restore recovery
is safe before dispatch; failed running tasks can have uncertain command outcomes.
Do not sum these streams as unique completed tasks or infer command delivery,
readiness, publication or promotion.

Reuse the existing process-local counter integer ceiling, pinned current and
comparison times, prior scheduler identity/up/uptime reads, freshness/boundary
checks, original scrape clock matching and one-second uptime advancement guard.
First samples are baselines. Changed/new/repeated processes, uncertain windows,
resets, missing/stale samples and source failures establish no delta. Missing is
never zero; independent partial positives survive sibling gaps. Comparable
samples retain exact differences including zero, not rates or exact event times.
Sampled continuity cannot exclude every intervening restart.

Read both metric families and their original timestamps in bounded closed frames
at each evaluation time, with exact up-target joins and duplicate/orphan checks.
All reads share the current three-second API budget; three protected HTTP reads
per verification poll remain unchanged. Keep the existing authorization and MFA
contract. Exclude commands, output, task/app identities, tokens, raw addresses and
provider errors. Invalid optional evidence is isolated from core health evidence.

Aggregate coverage and 18 ordered outcome signal summaries before the existing
32-target preview. Summary counts measure signals, not delta magnitudes or unique
tasks. New details yield first under 64 KiB API and 1 MiB report caps, preserving
aggregates and explicit omissions. Bounded 16-event/16 KiB history excludes clocks,
cumulative values and delta magnitudes. Sticky progress retains first/last
increases, observed baseline/gaps and every completion/recovery outcome. Never
sum overlapping poll deltas. Preserve qualification, exit behavior and polling.

Verify returned failure/timeout/cancellation/retry/recovery distinctions,
baselines, zero and exact deltas, resets/process uncertainty, sparse/stale/source
gaps, partial positives, closed dimensions, malformed and missing required fields,
role scope, independent transport decoding, bounded history/report fitting,
privacy and ownership, protected API behavior and generated SDK round trips.
