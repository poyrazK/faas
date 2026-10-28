# ADR-20261028101615 · Gregale Observe GA blockers: per-account alert evaluation, background work that follows plan and flags, and bounded entry points

- **Status:** proposed
- **Date:** 2026-10-28
- **Builds on:** [ADR-045](045-customer-alert-rules.md) (customer alert rules),
  [ADR-20261023115741](20261023115741-every-capability-behind-a-platform-flag.md)
  (every capability behind a platform flag),
  [ADR-20261024222102](20261024222102-observability-previews-release-and-operability.md),
  [ADR-745](745-custom-metric-history.md), [ADR-748](748-synthetic-checks.md),
  [ADR-749](749-alert-notification-channels.md),
  [ADR-20261022173833](20261022173833-custom-dashboards.md)
- **Decision:** Fix the code-side blockers an end-to-end review of Gregale
  Observe found:
  1. **Alert evaluation is per account.** A tick evaluated every account's
     rules in one goroutine, and delivered each customer webhook inline, with
     webhookout's retry policy (up to about 220 seconds for an unresponsive
     receiver), so one account's slow rules or webhook delayed every
     account's alerts. A tick now evaluates `AlertEvaluationWorkers` (8)
     accounts at once, each account's rules in order. Reading a rule's value
     gets `AlertObserveBudget` (15 s); a webhook delivery, retries included,
     gets `AlertWebhookBudget` (30 s); an account gets
     `AlertAccountTickBudget` (45 s) per tick, checked between rules, after
     which its remaining rules wait for the next tick, which starts with
     them. Persistence after a delivery uses the tick's context, so a
     delivery cut short is still recorded as failed. Webhook deliveries are
     no longer serialised: `webhookout.Dispatcher` only reads its options
     while dispatching.
  2. **meterd stops work an account's plan or flags no longer cover.** The
     evaluator ran every enabled rule and the synthetic runner probed every
     enabled check, whatever the account's plan or capability flags, so a
     downgraded account kept paying for synthetic wakes and kept being paged.
     meterd now decides, per account per tick:
     - alert rules run only on plans with alert rules, only while `alerts`
       and the metric's own capability (`AlertMetricCapability`: log search,
       custom metric history, SLOs, synthetic checks) are released, with the
       plan gates apid's create checks apply (custom metrics, per-app
       metrics);
     - notification channels deliver only while `notification-channels` is
       released (the rule still evaluates and fires to its webhook);
     - synthetic checks are probed only on plans with per-app metrics and
       while `synthetic-checks` is released.
     Skipped rules keep their state. A failed account read keeps the work
     running: a database hiccup must never silence alerts. Plans and
     organizations are cached for a minute. meterd becomes a declared
     consumer of these capabilities. `anomaly_alerts` is not decided here:
     its flag only refuses creating or changing a baseline rule (ADR-744).
  3. **Dashboard widgets follow their sources.** A widget read log counts,
     custom metric history or SLO status with plan checks only, so releasing
     custom dashboards exposed sources whose own routes stay hidden. A
     widget now reads only while its source's operator switch and capability
     are on, and otherwise shows the reason.
  4. **OTLP exports cost at most the per-app metric limit in writes.** Each
     distinct name in an export was written, including names the 5-metric
     limit then rejected, so a 1 MiB export could cost tens of thousands of
     database writes. apid reads the app's names once, writes points for
     known names, admits new ones in name order only while the app is under
     its limit, and rejects the rest without a write.
  5. **Channel test sends are rate limited.** Each is a real Slack post,
     PagerDuty event or email; an account may send
     `NotificationChannelTestsPerAccountMinute` (10) a minute, then gets 429
     `notification_channel_test_rate_limited` with the limit and Retry-After.
- **Operability:** `meterd_alert_rules_skipped_total{reason}` (`deferred`,
  `not_released`) and the synthetic loop's `not_released` outcome;
  `FaasAlertEvaluationSlow` (ticks averaging over 50 s for 15 minutes) and
  `FaasAlertRulesDeferred` (deferral for an hour), with the
  FaasCustomerObservability runbook.
- **Consequences:**
  - An account with more rules than fit its tick budget sees some of them
    evaluated a tick later; the deferral alert shows when that persists.
  - Alert rules and synthetic checks of capabilities that are off for an
    account stop. Deployments where accounts already use synthetic checks,
    SLO alerts, custom-metric or log-search alerts must release those
    capabilities to them before this ships, or the work stops.
  - The single-goroutine contract of `Evaluator.RunOnce` is replaced by
    per-account parallelism; evaluator state shared across accounts (the
    anomaly cache, the resume map, action execution) is guarded.
- **Rejected alternatives:**
  - *An asynchronous delivery outbox.* It moves delivery out of the tick but
    changes when deliveries are recorded and needs its own durability;
    per-account parallelism with budgets bounds the damage without
    changing what a tick records.
  - *Cancelling an account's tick mid-rule at its budget.* A fire claimed
    but not recorded would be lost; the budget is checked between rules.
  - *Disabling rules and checks when a plan changes.* It loses the
    customer's configuration and needs every plan path to call it; deciding
    at evaluation keeps them and covers capability flags too.
