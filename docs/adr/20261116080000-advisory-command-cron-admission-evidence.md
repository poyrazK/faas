# ADR-20261116080000 · Advisory command-cron admission evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-16
- **Extends:** [cron observation](20261017150710907-command-cron-admission-observation.md)
  and [app-task evidence](20261115210001-advisory-app-task-and-release-command-evidence.md).

App-task evidence begins after a task is materialized. Add optional
`command_cron_health` to explicit v2 runtime monitoring profiles to expose
unconsumed schedule boundaries and shared scan activity. Select `schedd` for
single-box and both `schedd` and `schedd-compute` for control-plane. Reuse
existing `schedd_command_cron_*` gauges. Add no database reads, metric producers,
alerts, execution changes, remediation or deployment gates. Preserve v1 and
unset profiles.

Require explicit scope `scheduler_ownership_scopes`. Ownership and legacy local
fallback follow existing producers; scopes may overlap. Compare observations
without summing schedules or inferring consensus. Counts describe schedules,
not catch-up occurrences, delivery acknowledgements or completed commands.
Retain ten ordered classes: `due`, `overlap_due`, `replacement_wait`,
`deployment_missing`, `exclusive_due`, `disabled`, `suspended`, `account_held`,
`not_due`, `unknown`.

Separate collection status, observer expectation, live sweep activity and each
durable class. Use the strict bounded class-dimension gauge reader with exact
up-target joins, paired complete frames, finite values and duplicate/orphan
detection. Read one closed selector and its original timestamps per selected
job, under the existing pinned time and shared three-second API budget. No up
targets adds no cron reads. Preserve expected missing targets and unknown
compute inventory.

Usable schedule values require fresh successful collection after the evidence
boundary, a fresh successful scrape and class clocks matching the collection's
original sample. Failed, never successful, stale or pre-boundary collection
cannot establish empty schedules. Valid partial positives survive sibling gaps.
Sweep activity and observer expectation are independently usable when durable
collection fails.

Per-class values retain `schedules` and included `unknown_age`. The last five
classes have only unknown ages. Retain `oldest_due_at` and
`oldest_known_due_age_seconds` only when some ages are known. Boundaries come
from canonical timezone/DST evaluation of the first unconsumed nominal schedule
boundary. They may precede the rollout and are not continuous eligibility,
execution delay, last successful command or a promise a command should run.
Empty and entirely unknown timing omit both fields. Keep exact-integer wire
bounds centralized in `pkg/api/limits.go`.

Sweep values retain explicit `running`, optional `last_started_at` and optional
`last_success_at`. Running requires a start; a successful scan requires a prior
start. During an active scan, last success cannot follow its start. Idle can
retain an earlier success after a later failed or interrupted scan; do not
infer its outcome. Missing clocks mean no observed start or success, not zero
latency. Success includes empty enumeration and owner lookup; it does not prove
admission of every command. The shared scan also includes inline HTTP work, so
activity alone cannot identify a command-runner failure. Introduce no new scan
deadline or progress verdict.

Aggregate scope coverage, per-class observations, due/inactive scope counts and
sweep activity before the 32-item preview. Due scopes contain any of the first
five classes; inactive scopes contain disabled, suspended, account-held or
not-due intent. A scope can contain both, and unknown remains explicit.
Nonempty includes any observed schedules, including intentional inactive
intent. Empty requires all ten classes observed empty. Prioritize due evidence,
then other nonempty, incomplete and empty scopes, oldest known due evidence
first. New detail yields first under the shared 64 KiB API and 1 MiB report
caps; preserve aggregates and explicit omissions.

Decode optional JSON independently, requiring explicit scope, class order and
false/zero fields. Malformed cron evidence must preserve other diagnostics.
Expose no schedule, app, account, command, payload or provider-error identities.
Markdown retains only aggregate scope evidence and observation history.

Retain state, class/sweep coverage and due/inactive scope changes in the shared
16-event, 16 KiB history. Routine clocks, ages and schedule magnitudes do not
create events. Progress retains first/last nonempty and complete empty
observations, sampled empty-to-nonempty transitions, empty-throughout status
and sticky gaps/unknown ages. Clipping cannot erase previous progress.
Disappearing schedule evidence does not prove admission, delivery, recovery or
command completion. Preserve qualification, exits and polling.

Verify all ten classes and intentional inactive intent, sweep start/success
timing including empty scans, failed/stale collection with independent activity,
unknown/mixed ages, partial positives, malformed frames and clocks, scope overlap,
inventory gaps, preview/report/history limits and ownership, optional JSON
isolation, protected API access and SDK round trips.
