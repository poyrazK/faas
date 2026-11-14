# ADR-20261114120000: Per-plan startup window on every cold boot

Status: Accepted
Amends: ADR-138 (amendment 2026-10-22, "the startup deadline is the cold-boot window")

## Context

ADR-138's 2026-10-22 amendment (Hunt 12 H12-10) capped every plan's startup
deadline at the spec §6.1 cold-boot window, 30 s. It had to: the deploy prime
already gave a long deadline its time, but ordinary wakes kept the 30 s
COLD_BOOTING budget, so an app that took 50 s to listen deployed, served from
its snapshot, and then failed every cold boot after its snapshot went stale.
The amendment noted that raising a cap again required extended cold-boot
wakes, which in turn required `handleAppWake` to stop waiting on the
scheduler's main loop.

The cap left slow-starting stacks unable to deploy at all: JVM/Spring, Rails,
and large Python apps on one vCPU. `app_startup_timeout` was the most common
deploy failure on production in the 14 days to 2026-11-13 (11 of the
failures, deploy-path review P1).

The prerequisite has since landed. Explicit app wakes run on schedd's work
pool (`dispatchExplicitWake`). Gateway wakes run on the coordinator's detached
leader. Async deliveries run on drain workers. No cold boot waits on the main
loop.

## Decision

1. Each plan has a startup window, `Limits.MaxStartupDeadlineS`:

   | Plan | Window |
   | --- | --- |
   | Free | 30 s |
   | Hobby | 30 s |
   | Pro | 60 s |
   | Scale | 120 s |

   `startup_deadline_s` may be set up to the window.
   `MaxAppManifestStartupDeadlineS` is 120 s, the largest window.
2. The default deadline does not change: Free 15 s, every other plan 30 s.
   A longer window is opt-in, so a broken app on Pro or Scale still fails in
   30 s unless its owner asked for more.
3. Every cold boot gets the app's deadline, not only the deploy prime:
   - The wake RPC budget (`budgetForWake`) becomes `35 s + extension`.
   - The watchdog's COLD_BOOTING reprieve becomes `30 s + extension`.
   - A WAKING row that fell back to a cold boot gets the same reprieve.
   - `extension` is `deadline + 15 s boot allowance − 30 s`, the existing
     `primeStartupExtension`. It is zero for deadlines within 30 s.
   - vmmd already enforced the deadline itself. Its image-healthcheck cap
     follows `MaxAppManifestStartupDeadlineS`.
4. The detached wake leader (`DefaultWakeLeaderBudget`) covers the longest
   window: 35 s + 105 s + 10 s = 150 s. The gateway's wake-generation floor
   follows from it. Each boot is still bounded by its own app's budget.
5. The gateway's request hold is unchanged by default (Free 10 s, paid 30 s).
   `wake_max_queue_wait_seconds` may now be set up to 120 s
   (`WakeQueueMaxWaitSeconds`), so an app with a long window can hold its
   first request through a cold boot. Without it, the first request gets the
   wake-in-progress 503 with `Retry-After`. The wake continues, and the retry
   joins it.

## Consequences

- An app accepted on Pro or Scale with a long deadline cold boots after any
  snapshot invalidation, as ADR-005 requires.
- A slow cold boot holds its COLD_BOOTING slot, RAM reservation and startup
  CPU boost for up to the window plus boot time. Admission already counts
  COLD_BOOTING instances (invariants 1 and 2), so it affects capacity, not
  correctness.
- The watchdog reads the app and its account for every COLD_BOOTING row older
  than 30 s, and for WAKING rows older than 35 s. Before, it read only when
  the row was a deploy prime. These rows are rare and short-lived.
- Release skew:
  - An older schedd with a newer apid accepts a 60 s deadline but kills its
    later cold boots at 30 s, which is the H12-10 failure.
  - Ship schedd (control plane) before apid accepts the new caps. The normal
    order is data, then control plane; apid and schedd ship together in the
    control-plane step, so roll schedd first within it.
  - Older CLIs reject the new values locally; nothing else is affected.

## Rejected alternatives

- **Snapshot-only wake for slow apps.** Rejected: it breaks ADR-005. Cold boot
  must always work.
- **Raise the spec window for every plan.** Rejected: it would make every
  broken app on every plan hold a slot for up to 2 minutes.
- **Gateway hold derived from the app's deadline.** Deferred: the gateway's
  route data does not carry the manifest. The explicit
  `wake_max_queue_wait_seconds` knob covers it for now.

## Tests

- `pkg/sched/cold_boot_window_test.go` pins the plan windows and checks that
  the RPC budget, watchdog, leader budget and queue-wait cap cover them.
- `pkg/sched/prime_startup_budget_test.go`,
  `pkg/sched/startup_window_wake_test.go`,
  `pkg/whycopy/startup_deadline_copy_test.go`.
