# ADR-20261121190000: Observe review round 8: capacity refusals count against customer availability, platform signals stay out of app errors

Date: 2026-11-21
Status: Accepted
Amends: ADR-082 (availability SLI), ADR-20261115194000 (platform alerts),
ADR-20261110090000 (class-based request series)

## Context

The Observe audit of 2026-11-21 (three code sweeps over metrics, wakes, logs,
log drains, errors, issues, alerts, SLOs, synthetic checks and the debugger)
found about twenty gaps. Most are bugs fixed in place. A few change what a
signal means and need a recorded decision:

1. **Capacity 429s were invisible.** Customer SLO availability, the app error
   rate, error-rate alerts, burn rate and the platform outage detector all
   measured 5xx over 2xx and 5xx. When an app hit its plan concurrency, its
   drop overflow mode, a full concurrency queue or a node refusal, the gateway
   answered 429 before the guest saw the request. An app refusing most of its
   visitors read 100% available and no alert or outage email fired.
2. **Platform failures filed as app exceptions.** A 5xx the guest never began
   to answer (a failed wake, a platform refusal) was recorded as an app error,
   grouped into an issue, counted as a new error fingerprint and paused the
   customer's failing cron, which emailed them to fix code that never ran.
3. **Platform alerts ignored plan gates.** The default check-failing and
   SLO-burn notices ran for checks and SLOs whose plan feature was off, and
   for checks whose runner had stopped, so the latest run was days old.

## Decision

1. **Capacity refusals are failed requests for the app.** The gateway marks a
   429 it answered, before the guest sent a byte, because the app had no
   capacity (codes `app_concurrency_reached`, `concurrency_throttled`,
   `concurrency_queue_full`, and the node's capacity refusal). It counts them
   in `gateway_capacity_refusals_total{app}`, pre-instantiated per app like
   the class series so `increase()` sees the first one. Customer SLO
   availability, `error_rate_pct`, burn rate, the metrics card error series
   and the outage detector add them to both the failed and the eligible
   counts. Other 4xx, including the customer's own rate limits, consumer
   plans and tenant budgets, stay outside the SLI as in ADR-082.
2. **The platform availability SLO and the status page are unchanged.** They
   read `gateway_platform_errors_total` (5xx the platform produced). An app
   at its own plan limit is the platform working as sold, not a platform
   outage; counting it would let one tenant's traffic burn the public budget
   and page on-call. Fleet capacity exhaustion already answers 503
   `capacity` and is counted there.
3. **Deployment request health is unchanged.** It gates rollouts per
   deployment, and a capacity refusal happens before a deployment is picked.
   Counting it would roll back a healthy revision for an app's plan limit.
4. **Unanswered 5xx are `wake_failed`.** The app errors recorder classes a
   5xx the guest never answered as `wake_failed`. Such rows are kept for the
   debugger but are not issues, not new fingerprints and do not page. The
   failing-cron pause ignores runs that failed in the wake, the version pin
   or binding the instance.
5. **Platform alerts follow the same gates as the runner and the SLO
   rollup.** A check or SLO whose plan feature is off gets no default notice,
   and a check whose latest run is older than
   `api.SyntheticCheckStaleIntervals` intervals is not judged failing.
6. **Operator alerts for silent failures.** `FaasPlatformAlertEmailsFailing`
   fires when meterd gives up on a platform alert email, and
   `FaasGuestSpansWritesFailing` when apid's spans writer fails every write.
7. **`oom_kills` is an alert metric.** It counts the app's workload OOM kill
   events (ADR-121 amendment) in the window, needs an app and allows every
   action, so a rule can roll back a release that runs out of memory.
8. **An app's own wake p95.** `gateway_app_wake_latency_seconds{app}` mirrors
   the fleet histogram per app (series only for apps that woke on the
   gateway, the same buckets). The metrics response adds `app_wake_p95_ms`
   and `app_wake_samples`; clients show it from 20 wakes and otherwise keep
   the fleet figure.
9. **Create-only archive keys for compute from the manifest.**
   `log_archive.compute_write_only` renders `faas_log_archive_write_only` for
   compute-only hosts, and with static auth they stage
   `compute_credentials_src` (a PutObject-only key) instead of the control
   plane's read-write key. The validator refuses static write-only without
   that key.

## Consequences

- An app that turns away visitors at its plan limit now shows the refusals as
  errors in its SLOs, error rate and alerts, and can receive an outage email
  ("server error (5xx) or a capacity refusal (429)"). Customers on small plans
  may see SLO burn they did not see before; the fix is a larger plan or
  concurrency setting, which the email and `gregale debug status` point to.
- Dashboards that compute availability by hand from the class series do not
  include refusals until they add `gateway_capacity_refusals_total`.
- The counter exists only on gateways running this release; during a mixed
  release the queries read zero refusals from older gateways, the previous
  behaviour.
