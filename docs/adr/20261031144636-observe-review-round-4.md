# ADR-20261031144636 · Gregale Observe review, round 4: counters add up across instances, paused checks stop paging, channel resolves are retried

- **Status:** proposed
- **Date:** 2026-10-31
- **Builds on:** [ADR-20261030214500](20261030214500-observe-review-round-3.md)
  (Observe review, round 3), [ADR-745](745-custom-metric-history.md)
  (custom metric history; amended here), [ADR-748](748-synthetic-checks.md),
  [ADR-749](749-alert-notification-channels.md) (amended here),
  [ADR-20261022193512](20261022193512-unified-incidents.md)
- **Decision:** A fourth code review of Gregale Observe found metrics that
  are wrong on the platform's own model (many instances, restored from
  snapshots), alerts that keep paging after their source stopped, a recovery
  notice that is lost to a short outage, and incidents and changes that drop
  out of view:
  1. **OTLP counters accept delta temporality and are totalled
     server-side** (amends ADR-745). Only cumulative sums were accepted, and
     data points carry no instance identity, so two instances of an app
     alternated their running totals in one series and a restore from a
     snapshot rewound a counter; Prometheus read every drop as a reset and
     inflated the rate. Delta monotonic sums are now accepted: an export's
     delta points for a name are summed, and the store adds the sum to the
     counter's stored total (`AddCustomMetricCounter`), so every instance,
     restart and restore adds to one monotonic series. A name that held a
     gauge restarts from the delta. Cumulative sums are still stored as sent
     and are documented as correct only for a single-instance app; one
     export cannot mix the two for a name. A delta export retried after a
     server error can count twice; that is documented.
  2. **Custom metric timestamps are bounded and never move backwards.** A
     data point stamped more than `CustomMetricMaxFutureSkewSeconds` (300)
     ahead of apid's clock, or beyond the int64 nanosecond range, is
     rejected: a future stamp kept a stopped pusher's value fresh until then,
     and an out-of-range one wrapped into the past. A value stamped before
     the stored one is dropped instead of overwriting it, so a delayed or
     retried export cannot move a gauge or counter backwards.
  3. **A paused or stale synthetic check stops driving its alerts.** The
     consecutive-failures metric counted the latest runs whatever their age,
     so pausing a failing check to silence a known outage kept paging every
     cool-down until the runs aged out (7 days). A paused check now leaves its
     rules `unknown`, and so does a check whose newest run is older than
     `SyntheticCheckStaleIntervals` (2) intervals.
  4. **Synthetic checks need a public app.** The public edge does not serve
     an internal app, so every probe of one failed. Creating or resuming a
     check on an internal app answers `409 synthetic_check_invalid`, and the
     runner skips the checks of an app made internal; item 3 then leaves
     their rules `unknown`.
  5. **A channel's recovery notice is retried for a day** (amends ADR-749).
     A fire is sent again after the rule's cool-down while the breach lasts,
     but a resolve was sent once, with three attempts over about three
     seconds, so a short Slack or PagerDuty outage at recovery left the
     PagerDuty incident open. A resolve a channel does not take is stored in
     `alert_channel_resolve_retries` (rule, channel, the observation and
     time it reports) and retried each tick once due, the wait doubling from
     a minute to at most an hour (`AlertChannelResolveRetryBackoffMax`), for
     `AlertChannelResolveRetryWindow` (24 h), while the rule is enabled,
     recovered and its account may use channels; a new fire of the rule
     clears it, since it would resolve the new incident. Every failed channel
     notice is audited as `notification_channel.delivery_failed`, and a
     resolve given up as `notification_channel.resolve_abandoned`, so failures
     stay visible after the channel's last error is overwritten.
  6. **A purged app's incidents stay readable.** Round 3 kept a deleted
     rule's episodes, but the incident list and detail resolved their app by
     id and treated a missing app as hidden, so once the app was purged after
     its restore window its incidents vanished (and every read logged a
     warning). A missing app now reads as no slug, visible only to the
     account's own view: not to an organization member's, whose project
     access can no longer be checked, nor to an app-bound key. The in-memory
     store now also cascades an app purge to its rules, as PostgreSQL does.
  7. **Dashboards and saved log searches are audited**:
     `dashboard.created`, `dashboard.replaced`, `dashboard.deleted`,
     `log_saved_search.saved` and `log_saved_search.deleted`, like every other
     Observe configuration object.
- **Compatibility:** the migration is **expand** (a new table). The OTLP
  change is additive: exports that were accepted still are, and delta sums
  that were rejected are now stored. Older meterd binaries ignore the retry
  table.
- **Consequences:** counters from multi-instance apps read correctly once
  their exporters send deltas (`OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta`,
  now the documented setting). Customers who relied on a paused check to keep
  a rule firing see it go `unknown`. The audit log gains one row per failed
  channel notice.
