# Command-cron admission

These internal warnings cover the path from a scheduled command's nominal cron
boundary to a durable occurrence/task or recorded policy decision. Execution and
release commands have separate [app-task diagnostics](FaasAppTasks.md). HTTP
cron admission, fire-now receipts and command completion are separate paths.

## Read the evidence

Use one `job="schedd", instance` at a time. Nonempty owner-node scopes match
exact placement, including neither remote nor unplaced apps. Legacy scopes use
the local fallback and include unplaced apps; legacy replicas can overlap.
Never sum replicas or treat local progress as fleet completion.

The independent observer reads all command schedules every 30 seconds under a
three-second budget. Scrapes use cached memory only. Successful empty reads
emit ten zero cohorts. Failure withdraws cohort counts and ages; the last
success time freezes. Data expires two minutes after read start. A successful
scrape does not prove observation succeeded. Check
`schedd_command_cron_observation_healthy` before interpreting workload gauges.
Shutdown withdraws observation expectation without claiming schedules drained.

| Class | Meaning |
| --- | --- |
| `due` | A nominal boundary remains unconsumed; no observed overlap/deployment gate. |
| `overlap_due` | Active prior work requires an overlap decision; no skip has yet been inferred. |
| `replacement_wait` | Unbound replace policy has active prior work. |
| `deployment_missing` | No live deployment with rootfs key and image digest was observed. |
| `exclusive_due` | An exclusive binding exists; lane readiness is not predicted. |
| `disabled` | Customer intent is disabled. |
| `suspended` | A suspension reason or deleted app exists. |
| `account_held` | Account status/abuse hold prevents admission. Past-due accounts remain active. |
| `not_due` | Next canonical boundary is in the future. |
| `unknown` | Schedule, timezone, timing, policy or binding evidence is invalid. |

Intentional disabled/suspended/account-held state takes precedence over timing
validation. `oldest_due_timestamp_seconds` is the first boundary strictly after
`last_fired_at`, or creation before the first fire, computed with the execution
parser's timezone/DST rules. A boundary equal to observation time is due. This
is unconsumed schedule history, **not continuous eligibility, cancellation
start, execution latency, last successful command or proof a run was owed**.
Deadline/coalescing rules can require recording skips rather than running work.
No missed-boundary enumeration or scheduler state writes occur in the observer.
`age_unknown` is a subset of schedules, not additional work. Disabled,
suspended, held, not-due and unknown cohorts intentionally have unknown ages.
Mixed known and unknown evidence retains the oldest trustworthy boundary.

Actual `schedd_command_cron_admissions_total{outcome}` returns distinguish queued,
exclusive pending, coalesced, overlap skip, missed deadline, stale cursor,
deployment unavailable, rejected, error, canceled and unknown. Queued/pending
prove admission only. A deployment-unavailable return requests suspension but
does not prove that the separate suspension write succeeded; inspect the
`suspended` cohort and bounded log evidence. Store errors are not policy skips.
`decisions_total` covers pre-admission and legacy fallback decisions. The
unknown return outcome is retained without using arbitrary returned strings as
labels. All counters initialize even before traffic.

`admission_lag_seconds` measures the returned occurrence evaluation time minus
its nominal boundary after successful admission, including committed skips and
coalesced history. The stores return evaluation time in `CreatedAt`; the
database row creation time can differ. It measures neither database commit
time, task execution latency nor continuous eligibility. Invalid/future timestamps increment
`admission_lag_unknown_total` without a fabricated zero observation.

## Triage and recovery

1. **Telemetry unavailable:** inspect collection success, last successful read,
   target health, database latency, clock skew and deployed binary/catalog
   versions. Missing metrics mean unknown, not an empty or healthy queue.
2. **Sweep stalled:** compare last start, last successful completion, running,
   duration and success/error/canceled counters. An empty scan counts as
   progress. A scan succeeds after enumeration and owner lookups even if an
   individual admission fails. The shared loop includes inline HTTP cron work;
   a long HTTP call can delay commands. Inspect scheduler loop and HTTP traces
   before attributing the stall to the command coordinator.
3. **Decision overdue:** inspect existing supported cron history and customer
   schedule policy: revision, enabled/suspended intent, timezone, last-fired
   cursor, start deadline, missed-run and overlap behavior. Compare the first
   unconsumed boundary with committed occurrence history. Do not infer every
   elapsed minute should execute. For missing deployments inspect deployment
   state and suspension logs. For bound commands inspect exclusive operation
   and fencing evidence. Older overlap history may need a persisted skip,
   rather than another task.
4. **Replacement waiting:** use app-task cancellation/lease diagnostics and
   teardown evidence. Nominal age does not date the cancellation request. Let
   the existing cancellation and fencing protocol acknowledge prior work.
5. **Admission errors:** sustained admission, lookup, store-unavailable or
   legacy errors need bounded-log and database triage. Policy refusals,
   unavailable deployments, cancellation and customer command verdicts are
   separate signals. Correct the underlying dependency/configuration issue and
   let normal cron scans resume.
6. **Unknown evidence:** inspect invalid/future schedule timestamps, timezone,
   policy and binding consistency through supported diagnostics. Expected
   intentional unknown ages do not trigger this warning. An unexpected
   `admissions_total{outcome="unknown"}` increase also needs code/store contract
   investigation; it cannot establish a successful admission.

Do not rewrite cursors/timestamps, delete occurrence history, replay customer
commands, bypass account/overlap/deadline policy or force an exclusive lease.
Resume through normal scheduler recovery after correcting the cause. Frontend,
rollout, deployment and customer-facing incident status are outside this change.
