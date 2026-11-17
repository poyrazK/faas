# ADR-20261116090001 · Observed app-task operation outcomes

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-16
- **Extends:** [sampled process continuity](20261115130000-sampled-daemon-process-continuity.md)
  and [app-task evidence](20261115210001-advisory-app-task-and-release-command-evidence.md).

Current task states and runner activity do not explain returned operation errors
between verification polls. Add optional advisory `app_task_operations` for v2
scheduler jobs, reusing `schedd_app_task_operations_total`. Include `restore`,
`renew`, `execute`, `destroy`, `complete`, and seven closed outcomes: `success`,
`error`, `canceled`, `lease_lost`, `empty`, `cancellation_pending`, `capacity`.
No producer, execution path, database read, alert, health gate or retry policy
changes. Preserve v1 and unset profiles.

Use scope `process_local_counter_observations`. These counters belong to
individual scheduler processes, not unique tasks or a shared fleet backlog.
Success means a returned call; execute success can include a customer command
verdict. Destroy success does not independently prove VM absence, and complete
success is an acknowledgement, not successful command execution. Separate
cancellations, lease loss and capacity refusals from operation errors.

Reuse the existing comparison evaluation time and already-read previous identity,
up and uptime evidence. Retain prior job results only for selected scheduler
jobs. Read a closed operation-filtered selector and its original timestamps at
the current pinned time and, when requested, the previous pinned time. Keep
exact up-target joins, closed operation/outcome dimensions, paired complete
frames, duplicate/orphan detection, finite exact integers and existing sample
bounds. No up targets adds no counter reads. All reads share the existing
three-second API budget; v2 verification still uses three protected HTTP reads
per poll and adds no polls.

First valid samples are baselines. Retain their cumulative values without
presenting historical failures as new activity. Comparisons require fresh
counter points at each evaluation time, valid boundary timing, unchanged sampled
process continuity, and counter timestamps equal to their corresponding uptime
sample timestamps. Uptime advance must track elapsed original scrape time
within the existing one-second process reset tolerance. Otherwise report an
unknown comparison. Sampled continuity cannot rule out every intervening restart;
this is not process attestation or an exact event log.

For comparable points, retain the exact nonnegative sample difference, including
explicit zero. Decreases mean reset and establish no delta. Changed or new
processes, repeated samples, uncertain windows, missing or stale points and
source failures establish no delta. Keep usable points for diagnosis without
inventing activity. Differences are not extrapolated rates or exact event times.
Use the centralized exact-integer counter ceiling in `pkg/api/limits.go`.

The producer initializes six outcomes but capacity can remain absent until its
first observation. Missing capacity series remain missing; absence at either
endpoint cannot establish zero capacity refusals. Assess all 35 signals
independently so positive error or cancellation evidence survives another gap.
Partial coverage stays explicit even when the only gaps are sparse capacity
series. Increased state means any valid positive difference, not an outage.

Aggregate signal coverage and per-operation category counts before the 32-target
preview. Summary category counts describe signals with an increase, not numbers
of calls. Retain individual point values and deltas only in the restricted
preview. Prioritize error increases, other increases, gaps and unchanged/baseline
targets. New detail yields first under the shared 64 KiB API and 1 MiB report
caps; aggregate evidence and explicit omissions survive clipping. Independently
decode the optional section and reject missing or null required zero fields,
forged deltas, invalid process proof, mismatched windows and malformed clocks.
Keep raw labels, endpoints, provider errors and command/task/app identities out
of artifacts. Markdown contains aggregate coverage and observation history only.

History retains aggregate comparison categories and coverage in the existing
16-event, 16 KiB budget. Sample clocks, cumulative values and delta magnitudes
do not create events. Progress retains first/last observed increases and sticky
flags for errors, cancellations, lease loss, capacity refusals, baselines and
incomplete evidence. Do not sum delta magnitudes across polls or infer successful
task completion or continuous error-free operation. Preserve health
qualification, exit behavior and polling.

Verify baselines with old failures, exact and zero differences, unchanged process
timing, resets, changed/new/repeated processes, uncertain windows, stale or
missing endpoints, sparse and newly observed capacity, partial positives,
malformed frames, role scope, preview/report/history limits and ownership,
optional JSON isolation, protected API access and SDK round trips.
