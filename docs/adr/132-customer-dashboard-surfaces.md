# ADR-132 · Customer-facing dashboard surfaces (usage dashboard + rate-limit indicator)

- **Status:** accepted
- **Date:** 2026-08-24
- **Issue / PR:** [#308](https://github.com/poyrazK/faas/issues/308), [#314](https://github.com/poyrazK/faas/issues/314)
- **Decision:** Ship two customer-facing surfaces — per-app/day
  usage dashboard (issue #308) and per-account deploy rate-limit
  indicator (issue #314) — as a follow-on to the platform-
  observability mega-PR. The mega-PR ships the durable infra
  (per-plan limits constant, the `Daily` field on the usage DTO,
  the `RateLimit-Remaining` header). The follow-on PR wires the
  handler + templates + chart rendering.

## Context

Issue #308's unanswered customer question today:

> "How much of my plan's GB-hour budget did each app consume
> yesterday?"

Customers today answer from the usage dashboard's "this month"
view (single aggregate). They can't tell which app is the
hot-spot, can't see the daily trend, and can't correlate a
spike with a recent deploy.

Issue #314's unanswered customer question today:

> "How close am I to my deploy rate limit?"

Customers today find out via a 429 with no `Retry-After` (or
via a support ticket). The dashboard doesn't surface the
"deploys used / limit / window resets" pill.

## Decision

### 1. Per-app/day usage series (issue #308)

`pkg/api/dto.go` gains:

```go
type DailyUsagePoint struct {
    Date          string  `json:"date"`           // YYYY-MM-DD
    GBHours       float64 `json:"gb_hours"`
    TopAppSlug    string  `json:"top_app_slug,omitempty"`
    TopAppGBHours float64 `json:"top_app_gb_hours,omitempty"`
}

type UsageSummaryResponse struct {
    // ... existing fields ...
    Daily []DailyUsagePoint `json:"daily,omitempty"`
}
```

`pkg/state/pgstore.go` + `pkg/state/memstore.go` gain
`UsageDailyForAccount(ctx, acct.ID) ([]DailyUsage, error)`
— a single SQL query that GROUP BY day + computes the top app
per day (window function or correlated subquery).

### 2. Per-account deploy rate limit (issue #314)

`pkg/api/limits.go` gains the `DeploysPerHour` constant per plan
(Free 10, Hobby 60, Pro 300, Scale 1200). The migration
`00413_account_rate_limits.sql` adds the
`account_rate_limits` table (account_id PK + deploys_used + window_start).

`cmd/apid/handlers/rate_limits.go` adds `GET
/v1/account/rate-limits` returning
`{deploys: {used, limit, window_resets_at}}`. The authlimit
middleware stamps `RateLimit-Remaining` + `RateLimit-Reset` on
every 429.

`pkg/dashboard/templates/usage.html` renders the per-day series
as inline SVG sparklines (no JS chart dep). `apps_list.html`
shows a "X / Y deploys used" badge next to the deploy button.

## Consequences

- Closed-set vocabulary at the SQL layer (the `DeploysPerHour`
  constant is the only place the per-plan numbers live — same
  precedent as `cron_limit_per_app`).
- The `RateLimit-Remaining` header is the customer-facing
  closure of the §11 rate-limit invariant: a customer
  approaching the limit sees the counter decrement before
  the 429 fires.
- Inline SVG sparklines (no JS dep) keep the dashboard's
  first-paint under 200ms even on a 30-day series.

## Out of scope (deferred)

- **Per-invocation cost on the dashboard** (issue #308 OOS) —
  requires the per-invocation metering pipeline.
- **Date-range filtering** (issue #308 OOS).
- **Per-app rate limits** (issue #314 OOS) — only per-account
  lands.
- **Plan upgrade nudges** (issue #314 OOS).

## References

- ADR-016 (closed-set label vocabulary).
- ADR-127 / ADR-128 / ADR-129 / ADR-130 / ADR-131 (sibling ADRs;
  same mega-PR boundary).
- ADR-066 (multi-host / compute_nodes — out of scope here).
- `pkg/api/limits.go` (existing per-plan constants — the
  `cron_limit_per_app` precedent is the closest analog).
- `pkg/dashboard/templates/usage.html` (existing usage view;
  the per-day series renders alongside).
