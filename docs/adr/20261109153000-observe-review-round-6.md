# ADR-20261109153000 · Gregale Observe review, round 6: an organization's apps follow the organization, fires keep trying, small windows don't page, the spend alert counts the whole bill

- **Status:** proposed
- **Date:** 2026-11-09
- **Builds on:** [ADR-20261104142830](20261104142830-observe-review-round-5.md)
  (Observe review, round 5), [ADR-749](749-alert-notification-channels.md)
  (amended here), [ADR-747](747-custom-slos-error-budgets.md),
  [ADR-20261018083536](20261018083536-customer-log-search.md),
  [ADR-20261022173833](20261022173833-custom-dashboards.md),
  ADR-123 (spend alert), ADR-1430 (billing currency)
- **Decision:** A sixth review of Gregale Observe found ten defects. They
  are fixed as follows:
  1. **An organization's app is judged by the organization's plan.** Rule
     release, the per-app and per-account rule caps, synthetic checks, SLOs,
     log context, log search and the request-row cap of app errors read the
     plan and status of the app's resource owner (`state.AppResourceAccount`),
     not of the member who created the rule. Before, a Free member's rule on
     a Pro organization's app was skipped, and a downgraded member's rules
     kept an organization's app unwatched. The per-account cap counts rules
     per billing subject (`Account.ResourceOwnerID`), so one member's rules
     no longer take from another's personal allowance. meterd caches each
     owner for a minute. If the owner cannot be read, the rule runs.
  2. **Long-lived responses record time to headers.** A Server-Sent Events
     stream or upgraded connection recorded its whole lifetime in
     `gateway_request_duration_seconds`. Minute-long streams then read as
     latency, breached latency SLOs and fired p95/p99 rules. Such responses
     (traffic-long-lived statuses and 2xx `text/event-stream`) now record the
     time until their headers were sent. They still count as requests, so
     request counts and error rates are unchanged.
  3. **A PagerDuty routing key is not replaced under an open incident.**
     The incident a fire opened is keyed to the old routing key's service.
     A resolve sent to the new key cannot close it. Replacing the key while
     an enabled rule using the channel is firing is now refused with 409,
     naming the rules. Disabling a firing rule sends its resolve to the
     current key, which then frees the key. Renames, and Slack and email
     destinations (which hold no incident), are not restricted. A resolve
     still being retried after a recovery is not checked. That window is at
     most a day.
  4. **Email channels need a verified address and follow it.** An
     unverified address could belong to someone else, so creating an email
     channel now needs the account's email to be verified. The sealed
     address is replaced at send time with the account's current verified
     address. A changed address therefore receives alerts, and an address
     that lost verification receives nothing (the channel records the
     error).
  5. **Teammates see an organization app's incidents and dashboards.**
     Incidents list the alert episodes of the organization apps the caller
     can view, alongside their own. Episode detail resolves another
     member's episode on an organization app. Notes and acknowledgements
     are stored under the episode's owner account. Dashboards read an
     organization app the caller can view, with the organization's plan.
     There is no new route or schema.
  6. **A channel fire that failed is retried while the rule fires.** A
     fire that every attempt failed to deliver was not sent again until the
     cool-down ended, often an hour. It is now retried in meterd's memory at
     30 s, doubling, at most 5 min apart. Retries stop when the rule
     recovers, is disabled or stood down, its channels are released, or its
     cool-down ends, when the next fire takes over. A meterd restart loses
     pending retries, which costs at most what the old behaviour did.
  7. **One account's slow action no longer blocks the others.** Actions
     (rollback, demote) ran behind one evaluator-wide lock, so a rollback
     waiting on apid held every other account's fires. The lock is removed.
     Executors are safe for concurrent use.
  8. **Share and percentile rules need ten requests.** On a scale-to-zero
     app, a window often holds a handful of requests. One failed wake out of
     three read as a 33% error rate and paged. A threshold rule on
     `error_rate_pct`, `cold_start_pct` or `latency_p50/p95/p99_ms` now
     fires only when the window holds at least
     `api.AlertRatioMinRequests` (10) requests. With fewer, the read is
     `unknown`. The floor is checked only when the threshold is breached,
     so quiet apps cost no extra query.
  9. **The spend alert counts the whole month's overage.**
     `account_spend_eur` and the `meterd_account_spend_eur` gauge read
     `meter.MonthToDateSpendCents`, the sum the spend cap compares: compute
     overage, live elected egress overage and the object-storage charge.
     Before, they read compute alone, so a bill that was mostly egress or
     storage never warned. The metric keeps its `eur` name (dashboards
     depend on it) and is in the billing currency (ADR-1430).
  10. **Crash lines count as errors.** The plain-text level heuristic
      (`pkg/loglevel`) now also recognises `[FATAL]`, `[CRITICAL]`,
      `level=fatal` and similar markers. It also matches line starts written
      by default loggers and crashes: Python's `ERROR:` and
      `Traceback (most recent call last):`, Go's `panic:` and
      `fatal error:`, and Node's `TypeError: …` and other uncaught
      exceptions. Before, log alerts and `logs --level error` missed exactly
      the lines a crash writes. Prose that only mentions "error" still does
      not count.
  Two smaller fixes ride along. The operator-only fleet metrics the preset
  catalog hides (`canary_stuck_step`, `canary_fleet_in_flight_high`,
  `safedeploy_audit_emit_failing`, `deployment_audit_gc_failing`) are
  refused on rule create and update: such a rule stayed degraded for good
  and took a slot of the plan's limit. A data migration corrects the
  cold-start and spend preset descriptions, which described snapshot wakes
  and RAM-only spend; the `presetwhy` explanations are fixed to match.
- **Not changed:** the log context is still read once for the webhook and
  once for channels per fire; alert webhook secrets still rotate without an
  overlap window; the SLO rollup still writes 0/0 hours; anomaly windows
  stay unbounded; the log archive bucket's 90-day lifecycle is still an
  operator step. These are recorded for a later round.
- **Consequences:** No schema change, OpenAPI change or SDK regeneration is
  needed; one data migration rewrites two preset descriptions. Users of an organization app may see rules start or stop running
  as their organization's plan, rather than their own, now decides. A
  customer whose rules paged on a few requests sees `unknown` instead.
  Spend alerts may fire earlier for accounts with egress or storage charges.
