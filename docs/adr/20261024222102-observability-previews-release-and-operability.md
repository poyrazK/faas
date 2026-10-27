# ADR-20261024222102 · Observability previews: the gaps capability flags leave, a Datadog gate, and operator alerts

- **Status:** proposed
- **Date:** 2026-10-24
- **Builds on:** [ADR-20261023115741](20261023115741-every-capability-behind-a-platform-flag.md)
  (every capability behind a platform flag),
  [ADR-745](745-custom-metric-history.md), [ADR-747](747-custom-slos-error-budgets.md),
  [ADR-748](748-synthetic-checks.md), [ADR-749](749-alert-notification-channels.md),
  [ADR-752](752-datadog-integration.md),
  [ADR-20261018083536](20261018083536-customer-log-search.md),
  [ADR-20261022172052](20261022172052-alert-log-context.md)
- **Decision:** ADR-20261023115741 releases the observability previews per
  account through their capability flags: their routes, dashboard pages and
  alert metrics follow the flag, and each feature's environment variable
  stays the operator's platform-wide switch. Three gaps remain, and this
  change closes them:
  1. **Datadog was ungated.** ADR-752 said the integration was dark behind
     `FAAS_DATADOG_INTEGRATION_ENABLED`, but nothing read it, and the
     `datadog-integration` capability owns no route: its destinations ride
     the log-drain and app-webhook routes. Every account could create them.
     apid now refuses creating a `datadog` log drain or app webhook, or
     switching one to Datadog, with 503 `datadog_integration_unavailable`
     while the switch is off and 403 `capability_not_released` while the
     capability is off for the account. Existing Datadog destinations keep
     delivering, and edits that leave one Datadog pass.
  2. **Log search work outside a request ignored the capability.** vmmd's
     runtime warn and error lines were indexed for every account while
     `FAAS_LOG_SEARCH_ENABLED` was on, and meterd attached the
     ADR-20261022172052 log context, with a link into log search, to every
     account's alerts. apid's runtime-line receiver now decides each app's
     account by the `log-search` capability, once per batch, and counts the
     lines it drops as `not_released`. meterd becomes a declared consumer of
     `log-search`: it follows the flag through its existing runtime-config
     watcher and attaches the context only for released accounts, reading
     the account (plan) and app (organization) once per fire, which each
     rule's cool-down bounds, so operators keep plan and organization rules.
  3. **The pipelines failed silently.** meterd's synthetic runner and SLO
     rollup export their health, apid exports whether it ingests log events,
     and six Prometheus alerts page when the loops, the log ledger, runtime
     forwarding or alert channels stop working.
- **Why:** An operator switch turned on to try a preview on one account must
  not open it, or its background work, to every account; a feature declared
  dark must actually be dark; and customers' synthetic results, SLO budgets
  and logs must not go stale without anyone being told.
- **Consequences:**
  - *Datadog compatibility.* A deployment where customers already use Datadog
    sets `FAAS_DATADOG_INTEGRATION_ENABLED=1` and releases
    `datadog-integration` to those accounts; otherwise new Datadog
    destinations are refused from this release on.
  - *meterd as a consumer.* `pkg/productcap.KnownConsumers` and the platform
    flag consumer attributes gain `meterd` (plan and organization), so a
    rule an operator writes for `log_search` stays valid.
  - *Health series.*
    - meterd: `meterd_observability_loop_enabled{loop}`,
      `meterd_observability_loop_last_pass_timestamp_seconds{loop}`,
      `meterd_observability_loop_passes_total{loop,outcome}` and
      `meterd_observability_loop_items_total{loop,outcome}`, for the loops
      `synthetic_checks` and `slo_rollup`.
    - apid: `apid_log_search_ingest_enabled`, which lets the vmmd forwarding
      alert tell a failure from the refusals apid answers while log search is
      off.
  - *Alerts* (group `faas_customer_observability`, runbook
    [FaasCustomerObservability](../runbooks/FaasCustomerObservability.md)):
    - `FaasObservabilityLoopStalled`: no error-free pass for 10 minutes
      (synthetic checks) or 30 minutes (SLO rollup).
    - `FaasObservabilityLoopDisabled`: a loop not running for 30 minutes,
      typically the SLO rollup without `FAAS_PROMETHEUS_URL`.
    - `FaasObservabilityLoopItemsFailing`: synthetic results that cannot be
      recorded, or SLO hours that cannot be computed. A customer's own
      failing check never counts.
    - `FaasLogLedgerWritesFailing`: apid's ledger writes failing.
    - `FaasRuntimeLogForwardingFailing`: vmmd's forwarding failing while apid
      accepts lines.
    - `FaasAlertChannelDeliveriesFailing`: an unseal failure, or every
      delivery of one channel kind failing for 30 minutes. One customer's
      broken destination does not page while others of its kind deliver.
  - All alerts are `severity: warn` and `public_status: internal`: a preview's
    data is stale, but customer workloads are unaffected.
- **Rejected alternatives:**
  - *Hand-written platform flags per preview.* The generated capability flags
    already carry the per-account decision; a second flag per feature would
    let the two disagree.
  - *Catalog settings for the Datadog destinations.* Settings are app fields,
    edge rule kinds, runtimes, manifest keys and domains; a direct check,
    as `alertMetricCapabilityProblem` does for alert metrics, keeps that
    vocabulary unchanged.
  - *Per-account decisions in vmmd.* vmmd has no database and does not know
    accounts; apid already loads the app for every batch.
  - *A distinct gRPC code from apid for "log search off".* Older vmmd treats
    anything but Unavailable or Unimplemented as a per-batch failure and would
    retry every second; an apid gauge distinguishes the cases without a
    version-skew change.
  - *Alerting on every failed channel delivery.* Customers misconfigure Slack
    webhooks and PagerDuty keys; the channel's status shows them their
    failure, and the platform needs paging only when deliveries fail
    regardless of the destination.
