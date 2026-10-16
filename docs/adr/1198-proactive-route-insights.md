# ADR-1198: Proactive route insights

- Status: Accepted
- Date: 2026-10-15
- Related: ADR-1187 (route awake report), ADR-1188 (learned cacheability), ADR-976 (route advisor), the debugger regression webhooks (trigger-based fan-out)

## Context

The awake report and learned cacheability answer two questions customers of
a scale-to-zero platform care about: what keeps my app from parking, and
which routes can the edge answer without waking it. Both are pull-only: a
customer has to run `gregale routes awake` or `gregale routes advise` to find
out, so the app that never parks because of an uptime monitor stays that way.

## Decision

- **Evaluator.** apid runs `pkg/apid/routeinsights` every
  `RouteInsightInterval` (6 h). It evaluates at most `RouteInsightMaxApps`
  apps that ran in the last `RouteInsightWindow` (24 h, from
  `usage_minutes`) on plans with request telemetry, and records two kinds of
  insight per route:
  - `kept_awake`: the awake report found a route called at a steady interval
    whose removal would save awake time, on an app awake for at least
    `RouteInsightKeptAwakeMinShare` (half) of the window;
  - `cacheable`: the advisor would cache the route and learned cacheability
    established a safe max-age.
  The evaluator runs in apid because both analyses are apid packages. One
  app's failure does not stop the others; apps on plans without telemetry
  only resolve.
- **State.** `route_insights` holds one row per app, kind, method and route
  with the message, the evidence and its active/resolved lifecycle. An
  observation upserts the row; an insight not observed for
  `RouteInsightResolveAfter` (24 h) resolves, so one quiet evaluation does not
  flap a notification.
- **Notification.** A trigger on `route_insights` enqueues
  `routes.insight.detected` and `routes.insight.resolved` deliveries for
  enabled app webhooks that subscribe to them, in the same transaction as the
  transition, with one delivery per subscription and transition. The existing
  schedd dispatcher signs, retries and dead-letters them; nothing new talks to
  the network.
- **Read.** `GET /v1/apps/{slug}/routes/insights` and `gregale routes
  insights` list active insights, then those resolved in the last seven days.

## Consequences

- A customer subscribed to `routes.insight.*` hears about an app kept awake
  by a poller, or a route safe to cache, within six hours, without running
  anything.
- Evaluations read retained telemetry and usage for up to 500 apps every six
  hours; the cap bounds the load.
- MemStore has no telemetry, so local development has no insights.

## Not decided here

Insights for existing cache rules that serve stale content, email or
dashboard delivery, and per-app opt-out of evaluation.
