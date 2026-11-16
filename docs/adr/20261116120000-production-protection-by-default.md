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
| Notification channel | `account-email` | `email`, the enrolling account's own verified address (ADR-749 allows no other recipient); an email channel the account already has for that address is reused |
| Alert rule | `availability-burn` | `slo_budget_burn` on `availability`, `gt` 14.4, window `1h` (the multi-window burn: 1 h above 14.4× and 6 h above 6×), channel only, to `account-email` |
| Alert rule | `latency-burn` | `slo_budget_burn` on `latency`, same thresholds |
| Alert rule | `health-failing` | `synthetic_check_consecutive_failures` on `health`, ≥ 2, to `account-email` |
| Release gate | `availability` | `slo` gate on `availability`, `ReleaseGateDefaultMaxBurnRate` (10) |

All values live in `pkg/api/limits.go` as `ProtectionBaseline*` constants.
The SLO and latency objectives are deliberately loose: they are meant to
page when something is clearly wrong, not to define the customer's real
objective. Customers tighten them by editing the objects.

Each object goes through the same store method, validation, plan gate,
capability flag and quota as the public endpoint. An object that cannot be
made yet is recorded as skipped with a reason, and retried:

| Reason | When |
|---|---|
| `plan` | the plan lacks the feature (Free has no SLOs or alerts) |
| `not_released` | the feature's capability flag is off for the account (`custom-slos`, `notification-channels`, `alerts`, `release-gates`) |
| `email_unverified` | the account's email is not verified |
| `name_taken` | the customer already has an object with the baseline's name; it is never taken over |
| `limit_reached` | the app or account is at its limit for that object |
| `needs_slo` / `needs_channel` | the SLO or channel the item depends on was not created |

A full quota skips the object instead of failing anything.

The release gate combines with what ADR-1446 already defaults: canary,
first-wake 5xx rollback and the post-release watch. Together they give
"roll back on SLO burn" without a new rollback mechanism.

Edge recipes are **not** created. They stay suggestions
(ADR-20261020105000), to be surfaced later in a weekly digest.

### 3. The health check is free

ADR-748 bills a probe like any request. A baseline check must not add a
line to the customer's bill, or "protected by default" becomes "billed by
default". So for checks the baseline owns:

- **apid, not meterd, probes it** (slice 3a). meterd's runnable list leaves
  out a check recorded as the app's created `health_check`. apid claims due
  baseline checks once per interval across replicas
  (`app_protection.health_probe_at`), publishes an ADR-847 route-probe
  challenge for the app's newest live production deployment, and sends the
  check's request through the local gateway with it. The gateway writes no
  request telemetry and no usage for a validated route probe. The result is
  an ordinary row in the check's run history, so the `health-failing` alert,
  the dashboard and `gregale synthetics get` read it unchanged. An answer
  the gateway cannot attribute to the deployment (it could not wake it, or
  refused the probe) is a failed run: that is the outage the check exists
  to catch. Without a configured route prober the check and its alert are
  skipped (`probes_unavailable`). This reuses ADR-847's audited trust model
  and adds no secret. The alternative, meterd signing a new header with a
  new key the gateways verify, was rejected for that reason.
- **a wake caused only by a platform probe is unbilled** (slice 3b). apid's
  challenge for a baseline probe carries `unbilled: true`; gateways keep
  the flag with the token. When such a probe's own request admitted the
  wake (not one that joined a wake already in flight), the gateway records
  `platform_probe_wakes` (instance, `woke_at`, `probe_done_at`) after the
  response. meterd subtracts each instance-minute's overlap with
  `[woke_at, probe_done_at + 30 s]` from its resident seconds, after the
  quiet-pause subtraction and never below zero; the 30 s covers the park
  grace, one 10 s reaper tick and the park. If a customer request lands
  inside that tail it is unbilled too, by a few seconds, in the customer's
  favour. A failed ledger write bills the wake, the safe direction. Rows
  are kept 7 days.
- **an instance woken only by a platform probe parks again straight after
  the probe** (slice 3b) instead of waiting out the idle timeout. schedd's
  reaper reads the last 10 minutes of probe wakes; an instance whose latest
  request is no later than the probe's end (+2 s) parks once 5 s have
  passed with nothing in flight. Its open flows are the gateway's
  keep-alive connections, so they are ignored for it, as for a superseded
  release's drain; a customer request after the probe moves its last
  request past the probe and the normal idle timeout applies. The app's
  floor still holds. Park already reuses the node's existing init snapshot
  instead of capturing a new one when one is usable (`captureInitOrReuse`),
  so this costs a destroy, not a snapshot write; a node without a usable
  init snapshot captures one as any park does.

Customers cannot change a check's path or interval (only pause, resume or
delete it), so a baseline check stays a baseline check until it is deleted.

### 4. The customer always wins

apid records each item in `app_protection_objects`: `created` with the
object id and the values it wrote, or `skipped` with a reason. A created
record is never replaced. An apid worker sweeps every 30 s and leases up to
20 due enrolled apps (`app_protection.next_reconcile_at`, `lease_until`), so
one replica works on an app at a time. An app without a live production
deployment, or with items still missing, is looked at again after 15
minutes; a complete or ineligible app after 24 hours. Enrolling (or
re-enabling) makes the app due at once. The reconciler is idempotent:

- missing or skipped item: try to create it;
- object deleted by the customer: **not** recreated; status reports
  "removed by you";
- object edited by the customer: left alone, and no longer counted as a
  baseline object;
- the reconciler never updates or deletes customer objects, and never
  overwrites an object with the same name that the customer created first.

Every create is an audit event (`protection.object_created`) with the app
and object, so support can explain where an alert came from.

### 5. Turning it off

`gregale app protection off <app>` (`PUT /v1/apps/{slug}/protection`
with `{"state":"off"}`; `protection: off` in `gregale.yaml` in slice 4b) sets
`app_protection.state` to `off` and deletes the baseline objects that still
hold the values the baseline wrote, alerts and the gate first. Edited objects
stay, because they are now the customer's. An SLO or health check that a kept
alert rule or release gate still references stays too, since deleting it
would delete that rule. The account email channel is never deleted; other
apps may use it. Records of deleted objects are forgotten, so
`protection on` re-applies the baseline; `on` also enrolls an app created
before protection existed. Both are audited (`protection.on`,
`protection.off`).

### 6. Status

`GET /v1/apps/{slug}/protection` returns the enrollment state and each
baseline item: `active`, `pending` (waiting for a live production
deployment), `skipped` (with the reason above), or `removed` (the customer
deleted it). A later slice adds `customized`.

`gregale app protection <app>` prints one line and then each item;
`gregale app <app>` prints the line as `production:` (nothing when the
account cannot read protection). `gregale status` gains it later:

```
Production: protected ✓ (SLO, health check, alerts, rollback)
Production: partly protected — health check removed (gregale app protection on to restore)
Production: not protected (gregale app protection on)
```

### 7. No double emails

ADR-20261115194000's outage notice is skipped for an app with an enabled
`slo_budget_burn` rule on an availability SLO (the baseline's
`availability-burn`, or the owner's own), the same way it is skipped for an
app with its own `error_rate_pct` rule. A full outage burns the budget faster than the
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
3. a. Baseline health check and its alert, probed by apid through the
      route-probe challenge (no telemetry or usage rows).
   b. Unbilled probe wakes and park-after-probe (gateway, meterd, schedd).
      Required before the flag is released to customers.
4. a. `PUT /v1/apps/{slug}/protection`, CLI `gregale app protection
      [on|off]` and the status line.
   b. The `gregale.yaml` `protection:` key.
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
