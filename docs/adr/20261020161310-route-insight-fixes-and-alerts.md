# ADR-20261020161310: Route insight fixes, dashboard panel and alerts

- **Status:** accepted
- **Date:** 2026-10-20
- **Related:** ADR-1198 (proactive route insights), ADR-1199 (cache rule
  evidence), ADR-1188 (learned cacheability), ADR-1191 (`@app` edge rules),
  ADR-749 (notification channels), ADR-123 (alert presets)

## Context

Route insights (ADR-1198) reach a customer only through the API, the CLI or a
`routes.insight.*` webhook. A customer who never subscribed a webhook does not
learn that an uptime poller keeps their app awake or that a route is safe to
cache. ADR-1198 left dashboard and email delivery undecided.

Every insight also ends in prose: "`gregale routes advise` drafts it",
"shorten the rule's max-age". The change is always one of a few edge-rule
edits the platform can already make, yet the customer has to translate the
message into commands. ADR-1199 left suggesting fixes for stale rules open.

## Decision

**1. Each insight carries the fix the platform would apply.** The list
response adds `id`, a `fix` object when one applies, and `fixed_at` /
`fix_action` once one was applied. The fix is planned when the insight is
read, from the insight's evidence and the app's current edge rules, so it
always describes the change that would be made now:

| Insight | Fix |
|---|---|
| `cacheable` | `create_cache_rule`: one enabled `@app` cache rule for the route's GET/HEAD requests with the measured safe max-age. |
| `kept_awake` | The same rule, when the route also has an active `cacheable` insight. Otherwise no fix: the caller has to change. |
| `cache_too_short` | Raise the serving rule's max-age to the measured safe max-age. |
| `cache_stale` | Lower it to the measured safe max-age, or disable the rule when no max-age was safe, then purge the route's cached responses. |

A fix never widens what a rule matches. When the serving rule's path is
exactly the route's glob, the fix edits that rule (`set_cache_max_age`,
`disable_cache_rule`). When the rule covers more routes, editing it would
change routes nothing measured, so the fix adds a narrower override
(`add_cache_override`) instead. The override copies the rule's host, headers,
match expression, mode and action, keeps GET/HEAD only, sets the route's path
and new max-age, and takes priority one below the rule. A rule at priority 0
cannot be overridden, and a broad rule with no safe max-age cannot be
narrowed by disabling it, so those insights carry no fix.

`fix.id` hashes the insight, the action and every value the fix would write,
including the target rule's last update. A caller sends it back to apply the
fix. If anything changed in between, the request fails with 409 and the
current fix, so nobody applies a change they did not see.

**2. Applying a fix uses the edge-rule and cache-purge cores.**
`POST /v1/apps/{slug}/routes/insights/{id}/fix` (`{"fix_id": …}`, deploy-write
scope, idempotent) creates or updates the rule through the same code as
`POST /v1/apps/{slug}/edge-rules` and `PATCH /v1/edge-rules/{id}`. Plan
quotas, validation, fleet convergence and audit events therefore apply
unchanged. The create and update handlers' bodies move into
`edgerules.Service.CreateRule` / `UpdateRule`, which the HTTP handlers wrap.
A stale fix then records a route-scoped purge through the
`DELETE /v1/apps/{slug}/cache?path=` core. A purge failure after the rule
change is reported, not rolled back.

On success the insight resolves with `fix_action` and `fixed_at`, which sends
the `routes.insight.resolved` webhook. Active `cacheable` and `kept_awake`
insights for the same route resolve with a `create_cache_rule`. Refill and
content evidence lags a change by up to a full window, so the evaluator does
not reopen a fixed insight until `RouteInsightWindow` has passed since the
fix. Webhook payloads gain `id`, `fix_action` and `fixed_at`.

`gregale routes insights APP` prints each fix, and
`gregale routes insights fix APP ID` applies one. The typed client, the Node
and Python SDKs and the OpenAPI spec gain the operation.

**3. The app page shows insights.** On plans with request telemetry, the app
detail page lists active insights with an "Apply fix" button, and insights
resolved in the last seven days with whether a fix resolved them. The button
posts the fix id under a dedicated CSRF token to
`/dashboard/apps/{slug}/routes/insights/{id}/fix`, which calls the same core.
The redirect then reports the outcome.

**4. Notification channels deliver new insights.** A new alert metric,
`route_insights_detected`, counts the app's insights that became active within
the rule's window (`6h`, `24h` or `7d`). It requires an app. A
"New route insights" preset (`gt 0`, `24h`, Hobby and up) puts it in the
dashboard's one-click preset grid. Bound email, Slack and PagerDuty channels
(ADR-749) then receive the notification. Channel messages gain optional notes,
and this metric fills them with up to five insight messages, so the email
says what was found rather than only a count. Customers who do not enable the
preset get no notification, which is the per-app opt-out.

## Consequences

- An insight is actionable from the API, the CLI, the dashboard and
  webhooks, and every path makes the same change.
- One-click fixes create enabled rules, unlike `routes advise --apply`, which
  creates disabled rules for review. An insight's evidence is a measured safe
  max-age over a full window, while an advice suggestion uses a default
  max-age. Every fix is an ordinary edge rule the customer can edit, disable
  or delete.
- A fixed insight stays resolved for at least one window even if the fix did
  not help; the next evaluation after that reopens it with fresh evidence.
- The metric's migration replaces `alert_rules_metric_chk` online and renames
  the widened copy back so later migrations find it by name. migration-lint
  classes a constraint rename as contract, so the alert ships with a full
  release; the fix columns and the dashboard ship as expand.
- The alert reuses meterd's evaluator, cooldowns and channel delivery; no new
  mail template or sender is added. The internal mail service's templates
  (ADR-2026101501) stay for platform-to-account mail.

## Not decided here

Insights on the Free plan, fixes for `kept_awake` routes that cannot be
cached (idle-timeout suggestions), and judging `vary_on` rules per variant.
