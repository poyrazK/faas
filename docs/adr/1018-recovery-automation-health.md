# ADR-1018: Recovery automation health

## Decision

Expose `GET /v1/apps/{slug}/recovery-policy/health` with app-read scope and MFA. Compute health from the existing consistent read-only queue snapshot, with no new queries, migrations, worker writes or notification deliveries. Reuse its current-version pending count, unresolved children across versions, retained execution charges and bounded entry sample.

Preserve named automation health. `gregale automations health --app APP` inspects the app recovery policy; adding `--name NAME` continues to inspect that automation's run statistics. Named time windows require a name. App health accepts a request-local `--stall-after-seconds` threshold (default 300, range 1–86400); it is not a saved alert policy.

Report paused first, then overdue worker evaluation, observed blockers, current budget or cooldown, incomplete sample, and finally inactive or healthy. For pending work, the newest retained pending evaluation demonstrates worker activity across the pending queue; without one, age is measured from preparation. Pending work behind an unresolved child is an expected serial-slot block, so untouched backlog entries do not independently imply a stall. Applied children remain inspected when automatic preparation is disabled. A currently exhausted budget or active cooldown is an expected block and does not make pending work stalled. Fresh running-child evidence does not become stalled merely because the child has run for a long time. Unknown evidence and inspection, admission, quota and slot waits remain explicit blockers.

Include the queue and its truncation indicator. An incomplete sample without an observed blocker reports unknown, never healthy. `sample_last_progress_at` is the newest retained queue-entry evaluation marking child creation, suppression or result recording. It may disappear when entries finish; it is not a durable app-wide success timestamp. Classification does not inspect workflow inputs, outputs or secrets.

## Consequences

Health is advisory. Healthy means no observed recovery blocker in this queue snapshot, not scheduler liveness, platform availability or execution authority. Stalled means an overdue evaluation, not proof that an execution exceeded its deadline. Reads never refresh worker observations, finalize a child, refund charges, release slots, suppress actions or change controls.

Alert thresholds, deduplication and recovery notifications remain a subsequent feature. This health view supplies one shared classification for future monitors without enabling notification traffic on read requests.
