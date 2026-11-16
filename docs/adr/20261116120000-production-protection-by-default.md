# ADR-20261116120000: Production protection by default

Status: Proposed
Date: 2026-11-16
Related: ADR-747 (SLOs and error budgets), ADR-748 (synthetic checks),
ADR-749 (notification channels), ADR-045 (alert rules),
ADR-20261019071734 (release gates on customer signals), ADR-1446 (safe
releases by default), ADR-1445 (post-release watch), ADR-20261115194000
(platform alerts for outages), ADR-986 and ADR-20261020105000 (edge recipes
and suggestions), ADR-1700 (platform flags)

## Context

Every piece a small team needs to run production safely has shipped:
SLOs with error budgets, synthetic checks, an email channel, alert rules on
SLO burn, release gates that judge a canary on an SLO or a check, and safe
releases with automatic 5xx rollback. All of it except safe releases is
opt-in. A customer gets protection only after creating four or five objects
in the right order, and the 2026-11-15 production rounds showed what
happens otherwise: a frontend served 5xx overnight and nobody was told.

ADR-20261115194000 closed the worst gap. meterd now emails the account when
an app serves at least 90% 5xx for 15 minutes. That catches total outages
of apps with traffic. It does not catch:

- partial degradation (5% errors, or slow responses), which burns an error
  budget long before it looks like an outage;
- an app with no traffic that has stopped answering (a parked app whose
  restore fails is only found by the next real user);
- a bad release judged on more than first-wake 5xx;
- the customer's own question: "is my production protected?"

Small teams will not set this up by hand. Protection has to be the default,
and turning it off has to be the deliberate step.

## Decision

A new public app gets a **protection baseline**: a small set of ordinary
SLO, check, channel, alert-rule and release-gate objects that apid creates
for it, which the customer can see, edit or remove like any other.

### 1. Which apps

An app is enrolled when it is **created** while the `protection_baseline`
platform flag is on for its account. Enrollment is stored in its own table,
`app_protection` (`state`: `on` or `off`; no row means `none`), so the wide
`apps` row and its many readers are unchanged; apps that existed before the
flag stay `none` and keep today's behaviour. They can opt in with
`gregale app protection on`. There is no backfill.

The baseline is applied when all of these hold, which mirrors ADR-1446:

- the app's `app_protection.state` is `on`;
- the app is request-mode, public (not `internal`) and not a PR preview;
- it has a live deployment in the default scope, which has passed its
  health check. Named project environments are out of scope.

### 2. What the baseline creates

| Object | Name | Value |
|---|---|---|
| SLO | `availability` | availability SLI, 99.5%, 30-day window |
| SLO | `latency` | latency SLI, 1000 ms threshold, 99%, 30-day window |
| Synthetic check | `health` | `GET` the app's health path (the deployment's `health_path`, else `/`), any 2xx, every 3600 s, timeout the default 10 s |
| Notification channel | `account-email` | `email`, the account's own verified address; for an org-owned app, the organization's billing email |
| Alert rule | `availability-burn` | `slo_budget_burn` on `availability`, fast burn (≥ 14.4× over 1 h and 5 m), to `account-email` |
| Alert rule | `latency-burn` | `slo_budget_burn` on `latency`, same thresholds |
| Alert rule | `health-failing` | `synthetic_check_consecutive_failures` on `health`, ≥ 2, to `account-email` |
| Release gate | `availability` | `slo` gate on `availability`, `ReleaseGateDefaultMaxBurnRate` (10) |

All values live in `pkg/api/limits.go` as `ProtectionBaseline*` constants.
The SLO and latency objectives are deliberately loose: they are meant to
page when something is clearly wrong, not to define the customer's real
objective. Customers tighten them by editing the objects.

Each object goes through the same store method, validation, plan gate and
quota as the public endpoint. A plan without a feature (for example, a plan
without synthetic checks) skips that object and the status says so. A quota
that is already full skips the object instead of failing the deploy.

The release gate combines with what ADR-1446 already defaults: canary,
first-wake 5xx rollback and the post-release watch. Together they give
"roll back on SLO burn" without a new rollback mechanism.

Edge recipes are **not** created. They stay suggestions
(ADR-20261020105000), to be surfaced later in a weekly digest.

### 3. The health check is free

ADR-748 bills a probe like any request. A baseline check must not add a
line to the customer's bill, or "protected by default" becomes "billed by
default". So for checks the baseline owns:

- meterd's runner marks the probe as a platform probe, the same way route
  probes are marked (ADR-847): no request telemetry, no usage row;
- a wake caused only by a platform probe is **unbilled**. If a customer
  request arrives on that instance, billing starts from that request;
- an instance woken only by a platform probe parks again straight after the
  probe instead of waiting out the idle timeout, so the platform's cost is
  about one restore per app per hour.

If the customer edits the check's interval or path, it is no longer the
baseline check and is billed normally (§4). The CLI says so on the edit.

### 4. The customer always wins

apid records each object it created in `app_protection_objects`
(app, kind, object id, the values it wrote). A reconciler runs after a
qualifying deploy and on a sweep, and is idempotent:

- missing object, never created: create it;
- object deleted by the customer: **not** recreated; status reports
  "removed by you";
- object edited by the customer: left alone, and no longer counted as a
  baseline object (for the check, this ends the free probe);
- the reconciler never updates or deletes customer objects, and never
  overwrites an object with the same name that the customer created first.

Every create is an audit event (`protection.object_created`) with the app
and object, so support can explain where an alert came from.

### 5. Turning it off

`gregale app protection off` (and `protection: off` in `gregale.yaml`) sets
`app_protection.state` to `off` and deletes the baseline objects that are still
unedited. Edited objects stay, because they are now the customer's.
`protection on` re-applies the baseline. Both are audited.

### 6. Status

`GET /v1/apps/{slug}/protection` returns the enrollment state and each
baseline item: `active`, `pending` (waiting for the first healthy deploy),
`skipped` (plan or quota, with the reason), `removed` or `customized`.

`gregale status` and `gregale app get` print one line:

```
Production: protected ✓ (SLO, health check, alerts, rollback)
Production: partly protected — health check removed (gregale app protection on to restore)
Production: not protected (gregale app protection on)
```

### 7. No double emails

ADR-20261115194000's outage notice is skipped for an app with an active
`availability-burn` rule, the same way it is skipped for an app with its own
`error_rate_pct` rule. A full outage burns the budget faster than the
outage detector fires, so the baseline alert arrives first.

### 8. Rollout

1. Flag `protection_baseline` off; internal accounts only.
2. New accounts.
3. All accounts (new apps only, as above).

## Consequences

- A team that runs `gregale deploy` once gets alerts by email, a health
  check and rollback on SLO burn, without configuring anything. Their
  status says so.
- Everything the baseline does is an ordinary object the customer can see
  and change. There is no hidden alerting path and no new rollback logic.
- The platform pays for an hourly probe wake on every enrolled app. The
  park-after-probe rule keeps that to one restore per app per hour. This is
  a capacity cost, so `meterd_platform_probe_wakes_total` is exported and
  reviewed before step 3 of the rollout.
- Existing apps are unchanged unless their owner opts in.
- Open: whether a parked instance can be released straight after a probe
  without writing a new park snapshot needs to be confirmed in the
  idle-park path before slice 3 lands; if not, the probe falls back to
  waiting for the idle timeout and the cost is re-measured.

## Slices

1. `app_protection`, `app_protection_objects`, the flag, `ProtectionBaseline*`
   constants, `GET /v1/apps/{slug}/protection`.
2. Reconciler creating SLOs, channel, alert rules and gate; outage-notice
   suppression (§7).
3. Baseline health check with platform-probe marking, unbilled probe wakes and
   park-after-probe.
4. CLI: `gregale app protection on|off`, the status line, `gregale.yaml`
   `protection:` key, and the "edited, now billed" warning.
5. Docs page and rollout.

## Alternatives considered

- **Billed hourly check.** No platform cost, but every scale-to-zero
  customer gets an unexplained charge. Rejected.
- **No default check.** Cheapest, but a parked app with no traffic would go
  unmonitored, the exact case the outage detector cannot see. Rejected.
- **Backfill existing apps.** Rejected for now; owners opt in.
- **Auto-create edge recipes in log mode.** Reverses ADR-20261020105000 and
  adds rules nobody asked for. Kept as suggestions.
- **Hidden platform-side protection** (alerts evaluated without customer
  objects). Simpler to build, but the customer cannot see, tune or turn it
  off, and it duplicates the alert engine. Rejected.
