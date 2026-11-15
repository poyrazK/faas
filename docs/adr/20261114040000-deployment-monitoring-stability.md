# ADR-20261114040000 · Optional deployment monitoring stability window

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [deployment timelines](20261113180002-post-deployment-monitoring-timeline.md)
  and [advisory notification history](20261114030000-deployment-notification-history.md).

Add opt-in `gregalectl obs verify-deployment --stable-for D`. Zero keeps the
existing first-passing-assessment behavior, report shape and polling. Bound the
duration by the existing one-hour verification maximum, reject a duration larger
than an explicit wait, and never extend an explicit or policy-derived budget.
Do not enable the option automatically in deployment workflows.

Require all three existing health checks to pass across a sampled window.
Measure progress as the minimum of local elapsed observation time and the
smallest source-clock advance since the first passing assessment. Retain local
monotonic times when available. Re-reading the same samples, changing API
generation timestamps or refreshing evaluations of unchanged probe runs cannot
qualify the window.

Track serving inventory, every expected probe's evaluation and actual run,
Alertmanager API and all seven pipeline observations, each aggregate
integration's attempt/failure observations, receiver collector/inventory, and
each expected receiver group's request/failure/success-observation clocks.
Historical success values remain acceptable for quiet routes: advance the
observation clock without requiring a new successful delivery. Direct probe
webhook/heartbeat evidence remains advisory, outside stability qualification.
Use only the two already fetched operator APIs; send no probes or notifications.

Restart the active window on degraded/unknown health, unavailable source vectors,
changed source identities or probe policies, source-clock regression, local
observation-clock regression, or polling gaps longer than the existing freshness
ceiling. Repeated waiting observations do not count additional resets. Changed
or regressed evidence that still passes starts a new baseline on that poll.
Track all pairs, not just previews. Source maps are bounded by existing serving
pair and notification integration/group limits; no new quota is introduced.

Expose a fixed-size optional `stability` summary in the schema-version-1 report
and human output: requested duration, state/reason, elapsed/source/observed
seconds, source count, window/last-observation times, resets and last reset
time/reason. Keep source identities private to the recorder. Copy snapshots so
later polls or terminal handling cannot rewrite earlier evidence. Disclose that
sampled stability does not establish continuous availability.

Keep the three check assessments and health timeline unchanged. When they pass
but stability is incomplete, hold the overall result at `unknown` with
`awaiting_stability`. At timeout expose `incomplete` stability and, if the checks
otherwise pass, `stability_window_incomplete` with exit 3. Preserve current
degraded/unknown health precedence and existing exit codes. Only raw degraded
health sets `had_confirmed_faults`. Cancellation and report-write failures mark
the summary interrupted without inventing a new source observation. If writing
crosses the budget, persist terminal status without another API poll.
Healthy evidence observed after the deadline cannot complete the stability window.

Keep reports within the existing 1 MiB envelope, including maximum primary and
advisory evidence. Test advancing versus repeated source clocks, every required
source, actual probe runs, quiet receivers, expectation/policy changes,
regressions, observation gaps, recovery and reset counts, timeout and interruption,
default compatibility, private CLI reports, advisory independence and the full
report envelope. Production convergence remains separate.
