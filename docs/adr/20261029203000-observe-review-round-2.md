# ADR-20261029203000 · Gregale Observe review, round 2: work stops with the app and the account, rules keep a destination, webhooks hear resolves

- **Status:** proposed
- **Date:** 2026-10-29
- **Builds on:** [ADR-20261028101615](20261028101615-observe-ga-blockers.md)
  (Observe GA blockers), [ADR-045](045-customer-alert-rules.md) (customer
  alert rules), [ADR-745](745-custom-metric-history.md),
  [ADR-747](747-custom-slos-error-budgets.md), [ADR-748](748-synthetic-checks.md),
  [ADR-749](749-alert-notification-channels.md)
- **Decision:** A second code review of Gregale Observe found work that
  outlives the app or account it serves, a way for a rule to lose its last
  destination, and contracts webhook receivers and OpenTelemetry exporters
  expect but did not get:
  1. **A deleted app's rules and SLOs stop.** Deleting an app starts a 7-day
     restore grace; its alert rules kept evaluating and delivering through
     it (synthetic checks already stopped). `ListEnabledCustomerAlertRules`
     and the SLO rollup's listing now skip apps with status `deleted`;
     restoring the app resumes them. Account-wide rules have no app and are
     unaffected.
  2. **A suspended account, or one pending deletion, gets no alerts, probes
     or SLO rollups.** Its apps do not serve, so probes only failed and paged
     it. meterd's release (ADR-20261028101615) treats such an account as not
     live; a past-due account still serves and keeps everything.
  3. **A channel that is a rule's only destination cannot be deleted.** The
     delete cascaded away the rule's binding and left it firing into
     nothing, although the API refuses to create a rule without a
     destination. The delete now fails with 409
     `notification_channel_in_use`, naming up to 20 such rules (no webhook,
     no other channel), until they get another destination or are deleted.
     The check and the delete run in one transaction that holds the
     channel row, so a binding insert to it waits.
  4. **Webhooks may receive resolves.** Only channels heard that a rule
     recovered. A rule with `notify_resolved` (`--notify-resolved`) also
     sends its webhook a signed `alert.resolved` event, with the same headers
     as a fire, when it recovers; every fire payload now says
     `"event": "alert.fired"`, the vocabulary MCP connection-health payloads
     already used. It is off by default and needs a webhook: receivers
     written before it count every delivery as a fire. A resolve takes no
     delivery-ledger row and runs no action; its outcome is counted in the
     tick's stats.
  5. **OTLP metric exports may be gzipped.** The OpenTelemetry Collector's
     HTTP exporter compresses by default, and those exports failed as
     invalid. `Content-Encoding: gzip` is decompressed, with
     `OTLPMetricsMaxBodyBytes` (1 MiB) bounding the body both as sent and
     decompressed; any other encoding answers 415.
  6. **Alert rule names are unique per app.** They were unique per account,
     while the error said "for this app", and a deleted app's rules held
     their names for its 7-day grace. `alert_rules_app_name_uniq (app_id,
     name)` and `alert_rules_account_wide_name_uniq (account_id, name) WHERE
     app_id IS NULL` replace `alert_rules_account_name_uniq`; presets keep
     appending the app slug to their names.
  7. **Alert deliveries are retained for 90 days.** The ledger grew without
     bound. Once per `AlertDeliveryPurgeInterval` (1 h) the evaluator deletes
     settled deliveries fired more than `AlertDeliveryRetentionDays` (90)
     ago, `AlertDeliveryPurgeBatch` (1,000) rows per statement and at most
     `AlertDeliveryPurgeBatches` (20) statements, oldest first, through a new
     `alert_deliveries_fired_idx`. A rule's newest delivery stays (MCP
     connection health and the delivery CLI read it), and so does a delivery
     with a rollback action, which would cascade away with it.
  8. **Docs say what the code does.** The alert webhook paragraph promised
     that a permanently failing endpoint is paused, which nothing does, and
     now describes the signature, retries and ledger. The synthetic check cost
     table used half the plans' RAM and the included GB-hours from before
     ADR-1430, so every figure was about half the real cost.
- **Migrations:** `20261030090000000_alert_rule_notify_resolved` (expand: a
  boolean column with a constant default), `20261030090000001_alert_rule_names_per_app`
  (contract: it drops the account-wide name index, so only a full release
  applies it; the new indexes are built concurrently first), and
  `20261030090000002_alert_deliveries_fired_idx` (expand, concurrent).
- **Consequences:**
  - Restoring an app resumes its rules and SLO rollups; the hours it was
    deleted have no rollup rows.
  - A customer who deletes a channel a rule depends on gets a 409 instead
    of a silent gap; the API, CLI and SDKs report the rules to fix.
  - Two apps of one account may now have rules with the same name. A
    rollback of the names migration fails while two do; rename one first.
  - Deliveries older than 90 days are gone from `gregale alert deliveries`
    and the dashboard; rollback evidence and each rule's newest delivery
    remain.
- **Rejected alternatives:**
  - **Disabling a deleted app's rules.** Restoring the app would then need to
    remember which rules were enabled before; filtering by app status needs
    no state.
  - **Resolves to every webhook by default.** Existing receivers would page
    again on every recovery.
  - **Letting a channel delete leave rules without a destination, with a
    dashboard warning.** The API already refuses to create such a rule;
    refusing the delete keeps the same invariant.
  - **Keeping names unique per account and fixing only the message.** Two
    apps commonly want the same rule names ("high-errors"), and presets
    already work around the account-wide index.
