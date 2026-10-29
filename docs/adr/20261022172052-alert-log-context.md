# ADR-20261022172052 · Alerts carry what the logs show

- **Status:** proposed
- **Date:** 2026-10-22
- **Builds on:** [ADR-20261018083536](20261018083536-customer-log-search.md)
  (log search over the ADR-213 ledger), [ADR-749](749-alert-notification-channels.md)
  (notification channels)
- **Decision:** When an app alert fires, the evaluator reads the app's log
  ledger for the alert window and attaches a compact log context: event
  totals by level and HTTP status, the route with the most error events,
  the deploys of the hour before the window, and a log search link. It
  travels in the webhook payload as `log_context` and as notes on Slack,
  PagerDuty and email notifications. Dark behind `FAAS_LOG_SEARCH_ENABLED`,
  which meterd now reads as well as apid.
- **Why:** An alert says a number crossed a threshold. The next questions
  are always "what errors are we seeing?" and "did something just ship?".
  The ledger already answers both — HTTP events, runtime warnings and
  errors, and (since slice C) build and deploy events — and the facet query
  already bounds the cost. Putting the answer in the notification is the
  Datadog "related logs and changes" step without a new store or a separate
  correlation service.
- **Consequences:**
  - The window is the rule window, capped at 24 hours (the text search
    bound); deploys are read from the window plus the hour before it, at
    most three, newest first.
  - Three bounded ledger reads per fire event (two facet counts, one deploy
    list) under the 5 s search statement timeout. Fires are rate-limited by
    each rule's cooldown, so this adds no steady load. Any read failure
    omits the context; the alert itself never waits on or fails for it.
  - Only fire events carry the context; resolve and test deliveries do not.
  - Account-wide rules (no app) carry none: the ledger is app-scoped.
  - The link is a dashboard path (`/dashboard/apps/{slug}/log-search?…`);
    notes render it as a URL only when the dashboard base URL is set.
- **Rejected alternatives:**
  - *Linking only, without counts.* The reader still has to open a page to
    learn whether the logs say anything; counts in the notification answer
    it at a glance.
  - *Sampling log lines into the notification.* Lines can carry customer
    data into Slack and email, beyond the redaction the ledger already
    applies, and get long; counts and a route name do not.
  - *A correlation table written at fire time.* Duplicates the ledger the
    context is read from.
