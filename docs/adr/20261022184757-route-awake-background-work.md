# ADR-20261022184757: Background work and waitUntil in the awake report

- **Status:** accepted
- **Date:** 2026-10-22
- **Related:** ADR-1187 (awake report), ADR-1199 (probe and prewarm sources),
  ADR-078 (waitUntil), ADR-1198 (route insights)

## Context

The awake report (ADR-1187) attributes an app's billed running time to the
routes whose requests restarted its idle timer. ADR-1199 added route probes
and scheduled prewarms as sources for minutes no request explains. Everything
else was reported as "unattributed: min instances, cron, queue, waitUntil".

For apps that do background work, that line was often the largest. Cron
firings, queue deliveries, delayed tasks, async invokes, inbound webhooks,
replays and event-source deliveries reach the instance through schedd's
dispatcher (`EnsureWake`), not through the gateway, so none of them appears
in request telemetry. Customers could see the app was awake but not which
cron job or queue kept it so.

`waitUntil` (ADR-078) work runs after a response, and the reaper does not park
an instance while it has tasks in flight (`instances.tail_count`). No per-task
history is stored, only the live count.

## Decision

**1. Invocations become awake sources.** The report reads the app's
`invocations` whose delivery (`received_at` → `completed_at`) overlaps the
window. Each delivery counts as running in every minute it spans. In-flight
deliveries run to the end of the window, and any delivery is capped at
`RouteAwakeWorkMaxMinutes` (60). Like probes and prewarms, a source keeps the
app awake for the idle timeout after its last running minute, and a minute is
credited to it only when no customer request explains that minute.

Sources are the invocation kinds: `cron`, `queue`, `delayed_task`,
`async_invoke`, `inbound_webhook`, `replay` and `esm`. Cron firings are
reported per cron job, named by the path or command it runs and its schedule;
queue deliveries are reported per queue. A source's `events` count the
deliveries that started in the window.

A new partial index, `invocations (app_id, received_at) WHERE received_at IS
NOT NULL`, built concurrently, keeps a seven-day read bounded.

**2. waitUntil counts toward the request that registered it.** An instance
parks no earlier than the later of the idle timeout and its last waitUntil
task, and a task can run at most the plan's `TailTimeoutS` (5, 15, 30 or
60 s). The window a request keeps the app awake is therefore the longer of the
two whenever the plan enables waitUntil. The response reports
`wait_until_timeout_seconds`. The minutes go to the request's route, which is
what the customer changes to reduce them. At today's limits the
minute-resolution window (the idle timeout rounded up, plus a minute) already
covers a 60 s tail, so the rule changes no current figure; it keeps the report
right if a plan's waitUntil timeout ever outgrows its idle timeout.

**3. A cron job that never lets the app park gets a suggestion.** When a cron
source fires at a steady interval shorter than the idle timeout, the report
suggests running it less often than the idle timeout, the same way it
suggests it for a polled route.

## Consequences

- The "unattributed" line now holds only min instances, probes older than a
  day, invocations already pruned by result retention, and telemetry the
  plan's rate cap dropped.
- Route what-ifs already treat every source as cover, so removing a route no
  longer claims minutes a cron job or queue would have kept awake anyway.
- The index adds one btree entry per dispatched invocation.
- Retries of one invocation are counted by their last delivery only; the
  per-attempt history covers async invokes and replays alone.

## Not decided here

What-if estimates per background source, route insights for cron jobs and
queues, and attributing workflow steps and jobs that run outside the app's
instances.
