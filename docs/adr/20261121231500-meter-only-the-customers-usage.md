# ADR 20261121231500 · Meter only the customer's usage, and all of it

- **Status:** accepted
- **Date:** 2026-11-21
- **Amends:** ADR-20261116120000 §3 (route probes), ADR-20261104132753
  (realtime egress, build minutes), ADR-20261116192126 step 1 (managed Redis
  handoff), ADR-20261103121407 (held usage), ADR-049 §B.3 (storage view)
- **Follows:** ADR-20261121141532

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Read-only audits of meterd after ADR-20261121141532 (rounds 3–8,
2026-11-21) found usage counted that the customer did not cause, and usage the
customer caused that is never billed:

1. **Route probes are customer usage.** A canary or protection-baseline probe
   is unbilled for its wake, but each probe request and its response bytes
   still land in `usage_minutes` as customer requests, cold boots and
   `tx_bytes`. The 2026-11-15 aborted rollouts showed 57,600 probe requests a
   day on one app.
2. **Infra build failures spend build minutes.** The monthly build-minute
   quota sums every build, including those the platform failed
   (`failure_class = 'infra'`), and refuses deploys once it is spent.
3. **Managed Redis handoff bills two VMs.** A resize or upgrade starts the new
   VM as an ordinary deployment beside the serving one; both are billed until
   the old one stops (up to 15 minutes), although step 1 says only the serving
   VM is.
4. **Realtime egress lost at month end.** The realtime egress sync reconciles
   only the current month. A node reports an hour for up to 24 h after it
   ends, so the previous month's last hours are never metered. A report for an
   endpoint deleted since is dropped.
5. **Paid hours dropped after a subscription ends.** The pusher skips every
   window of a subject whose *current* plan is Free, before it reads the
   hour's plan from the plan history. A paid hour still pending when the
   subscription ends is silently never billed.
6. **Usage views.** The app summary's `builder_seconds` was the app
   instances' CPU time, not builder time. The snapshot storage view counted
   the guest's nominal RAM (1,024 MB for a 1 GB app against ~150 MB on disk)
   and kept counting deleted apps.

## Decision

1. gatewayd does not record a route probe's request or response bytes in the
   per-instance usage stream. Telemetry and the wake-billing rules of
   ADR-20261116120000 are unchanged.
2. The build-minute quota leaves out builds with `failure_class = 'infra'`.
3. For an app with a managed Redis cache, an instance's billed residency
   starts when the serving VM of an older deployment stops, at most
   `api.ManagedValkeyHandoffWindowS` after the new VM started.
4. While a report for the previous month's last hour can still arrive
   (`api.RealtimeUsageReportMaxAge` + 1 h), the realtime egress sync also
   reconciles the previous month, writing at its last minute so the bytes
   stay in the month they were delivered in. A report for a deleted endpoint
   is attributed to the owner its earlier usage rows recorded.
5. Pending usage windows list a now-Free subject's hours from before the
   plan change that made it Free (its `plan_changed_at`, when after its
   `billing_from`); other Free subjects still list nothing. Such a window
   whose hour was on a paid plan, by a recorded return to Free after it, is
   held as `subscription_ended`
   (`meterd_billing_usage_held_windows`), so the existing expiring-held alert
   lets an operator bill, settle or waive it. It is delivered if the subject
   subscribes again within the lookback.
6. `builder_seconds` sums `builder_usage` for the app's builds that finished
   in the window. The storage view counts `snapshots.stored_bytes` when
   recorded and skips deleted apps.

## Not changed

- Public HTTP responses are not egress on hosts without public forwarding
  (R12-1), and edge cache responses are: which counter bills public
  responses remains the owner decision recorded in ADR-20261114100000.
- Managed PostgreSQL on an unreachable database node keeps its compute rate
  open. Database nodes have no heartbeat, and parking or failing over on
  unreachability risks two primaries; this needs a liveness signal first.
- A DRAINING instance kept by a runtime-config refresh is billed while it
  still serves the customer's open requests; a dead node is clamped by
  ADR-20261121141532.
- App tasks bill at the app's RAM when the minute is rolled, live migration
  keeps one network checkpoint per instance, and catch-up minutes use the
  current app configuration. Each is bounded (task timeout, one departed
  reading, 72 h) and needs a schema change to remove.

## Consequences

- Customer usage views and the CLI no longer show probe traffic; dashboards
  that counted probes as requests drop.
- A platform build outage no longer locks a customer out of deploying for
  the month.
- Paid usage pending at cancellation is visible and alerting instead of
  silently waived.
