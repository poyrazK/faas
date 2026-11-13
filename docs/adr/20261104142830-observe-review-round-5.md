# ADR-20261104142830 · Gregale Observe review, round 5: every incident end is announced, synthetic probes share fairly and skip the cache, plan caps hold after a downgrade

- **Status:** proposed
- **Date:** 2026-11-04
- **Builds on:** [ADR-20261031144636](20261031144636-observe-review-round-4.md)
  (Observe review, round 4), [ADR-745](745-custom-metric-history.md),
  [ADR-748](748-synthetic-checks.md) (amended here),
  [ADR-749](749-alert-notification-channels.md) (amended here),
  [ADR-20261018083536](20261018083536-customer-log-search.md),
  [ADR-20261022173833](20261022173833-custom-dashboards.md),
  [ADR-20261028101615](20261028101615-observe-ga-blockers.md),
  ADR-122 (edge cache, D3: requests with credentials bypass it)
- **Decision:** A fifth code review of Gregale Observe found incidents left
  open in PagerDuty, a probe runner one account could stall for everyone,
  checks the edge cache could pass, counters that lost sparse increments,
  unbounded dashboard reads, plan caps that a downgrade escaped, silent log
  drops and channels that could not be rotated:
  1. **Every end of an alert episode sends a recovery notice** (amends
     ADR-749). A resolve went out only on a tick that took a rule straight
     from `firing` to `ok`. An episode stays open through `degraded` and
     `unknown` reads, so a single failed Prometheus or database read during
     an incident meant the recovery was never announced, though the
     incident read resolved. A recovery is now announced whenever it closes
     an open episode. A rule that ends its episode without recovering is
     *stood down*: disabled while firing, no longer covered by the account's
     plan, flags or status, or with its app deleted (those were skipped with
     their state frozen, leaving the incident open for ever), and deleted
     while firing, directly or through its SLO, synthetic check or app. The
     evaluator stands down the rules it no longer evaluates (it lists the
     not-ok disabled ones and those of deleted apps each tick): it queues a
     notice for every channel bound to the rule while an episode is open,
     moves the rule's undelivered resolve retries into that queue, sends the
     webhook's `alert.resolved` with a `reason` and a null `observed` when
     `notify_resolved` is on, and turns the rule `ok`, which closes the
     episode. A deleted rule's notices are queued by the
     `alert_rules_channel_stand_downs` BEFORE DELETE trigger, named to run
     before `alert_rules_keep_episodes` closes the episode it looks for; it
     queues nothing when the account itself is being deleted. The queue
     (`alert_channel_stand_downs`) carries the rule's name and metric and has
     no foreign key to the rule, so a notice outlives it. Notices go out
     whatever the account's plan (they close what a fire opened), with the
     reason as their text, and are retried like round 4's resolve retries
     (a minute, doubling, at most an hour apart, for a day; abandonment is
     audited with the reason). A new fire clears a rule's queued notices.
     MCP readiness rules keep their own incident lifecycle and are not
     stood down.
  2. **Synthetic probes are scheduled fairly** (amends ADR-748). A pass
     waited for every due probe, eight at a time, and probes may take 30
     seconds, so one account's hanging app (a Scale account may have 500
     checks) stalled every account's checks for over half an hour, and
     round 4's staleness rule then turned their alerts `unknown`. A pass
     now starts the due checks a slot is free for and returns: meterd runs
     32 probes at once (`syntheticCheckConcurrency`) and at most
     `SyntheticCheckProbesPerAccount` (2) per account, the most overdue
     check first, and a check being probed is never started twice. Waiting
     checks are counted as `deferred`, and
     `meterd_observability_loop_backlog_seconds` reports how late the most
     overdue one is; `FaasSyntheticChecksBacklogged` pages after 15 minutes
     over 5 minutes late.
  3. **Synthetic probes bypass the edge cache.** A probe is an
     unauthenticated GET or HEAD, exactly what the edge cache answers,
     including with a stale copy when the app fails (stale-on-error), so a
     check on a cached path passed while the app was down. Every probe now
     carries the cookie `gregale_synthetic_probe=1`; a request with a cookie
     is never cache-eligible (ADR-122 D3), and the gateway strips only its
     own named routing cookies. No new bypass is created: any client can
     already skip the cache with a cookie.
  4. **Counters stay exported while idle.** The exporter dropped every
     series five minutes after its last push, counters included, and an
     OpenTelemetry delta exporter sends nothing for an idle counter, so a
     counter that moved less often than every few minutes lost each
     increment to `rate()` (no earlier sample in the window). A counter's
     total is Gregale's own and stays true, so counters are now always
     exported; gauges keep the freshness rule. A series' first increment
     still only starts it, so the docs ask apps to add 0 at start.
  5. **Dashboard data reads take a per-account slot.** A read resolves up
     to 24 widgets, each several Prometheus queries over up to 7 days,
     against the Prometheus that alert evaluation and SLO budgets read, and
     nothing bounded how many an account ran at once. An account now has
     `DashboardDataReadsPerAccount` (2) reads in flight per apid; another
     answers `429 dashboard_data_busy` with `Retry-After`. Results are not
     cached, because what a caller may see depends on its key.
  6. **Plan rule caps hold after a downgrade.** The API checks the per-app
     and per-account rule caps only when a rule is created, so a downgrade
     left every rule the old plan allowed evaluating and delivering. meterd
     now applies the account's caps to its enabled rules: per app and per
     account the oldest keep running and the rest are skipped as
     `over_plan_limit` (and stood down if firing), so a customer chooses
     which run by disabling others. Caps are applied per rule owner, which
     is never stricter than the API's billing-subject count.
  7. **The account log budget leaves a marker.** Runtime lines past an
     account's 600 per minute were dropped silently (vmmd's per-app budget
     writes a `system` line, the account budget did not), so a search or a
     `log_search_matches` count looked complete. apid now writes one
     `system` line per app and minute when the account budget drops its
     lines, outside the budget it reports, deduplicated across apids by its
     source event id. The alerts docs say log counts are capped.
  8. **Notification channels can be updated in place** (amends ADR-749).
     `PATCH /v1/notification-channels/{id}` (and `gregale alert channel
     update`) renames a channel or replaces its Slack URL or PagerDuty key,
     sealed like a new one, keeping its id and rule bindings; before, a
     leaked destination meant unbinding every rule, deleting, recreating and
     rebinding. The kind never changes; an email channel can only be
     renamed; a PagerDuty region changes only with a new key, as the stored
     key is sealed. A new destination clears the last error. The change is
     audited as `notification_channel.updated`.
- **Compatibility:** the migration is **expand** (a new table, a trigger
  function and trigger). An older meterd ignores the queue; rows wait until
  a new one delivers or abandons them. The PATCH route and SDK methods are
  additive; the webhook resolve gains an optional `reason` and may carry a
  null `observed` only for stood-down rules.
- **Consequences:** PagerDuty incidents close when their alert rule stops
  for any reason. A customer whose checks outnumber two probes at a time
  sees their own checks run later when their app is slow, never another
  account's. Downgraded accounts see some rules skipped; the docs explain
  how to choose. Idle counters add one series each to the exporter.
