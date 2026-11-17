# Production protection

Preview: available to accounts with the `protection-baseline` capability.

A new public app is protected by default. After its first live production
deploy, Gregale creates a small set of ordinary objects for it: two SLOs, an
hourly health check, an email channel, three alert rules and a release gate.
You get an email when the app degrades or stops answering, and a release
that burns the availability budget is rolled back, without configuring
anything.

Every object is one you can see, edit or delete with the usual commands.
There is no hidden alerting path.

## Check whether an app is protected

```sh
gregale app protection shop
```

```
Production: protected ✓ (SLOs, health check, alerts, rollback)
  availability SLO     active    availability
  latency SLO          active    latency
  health check         active    health
  email alerts         active    account-email
  availability alert   active    availability-burn
  latency alert        active    latency-burn
  health alert         active    health-failing
  rollback gate        active    availability
```

`gregale app get shop` prints the same first line as `production:`. The API
equivalent is `GET /v1/apps/{slug}/protection`.

Each item is in one of these states:

| State | Meaning |
|---|---|
| `active` | The object exists and still has the values Gregale set. |
| `pending` | Waiting for a live production deployment that passed its health check. |
| `skipped` | Could not be created yet; the reason is shown and Gregale retries every 15 minutes. |
| `removed` | You deleted the object. Gregale does not recreate it. |

Skip reasons:

| Reason | What to do |
|---|---|
| `plan` | The plan has no SLOs or alerts (Free). Upgrade to protect the app. |
| `not released` | The feature is not available to your account yet. |
| `email unverified` | Verify your account email; alerts go only to that address. |
| `name taken` | You already have an object with that name. Gregale never takes it over. |
| `limit reached` | The app or account is at its limit for that object. Free one up. |
| `needs slo`, `needs channel` | The SLO or channel the item depends on is missing. |
| `probes unavailable` | This deployment cannot run the health check. |

## What is created

| Object | Name | Value |
|---|---|---|
| SLO | `availability` | Availability, 99.5%, 30 days |
| SLO | `latency` | Requests under 1000 ms, 99%, 30 days |
| Synthetic check | `health` | `GET` the app's health path (else `/`), any 2xx, every hour |
| Notification channel | `account-email` | Your verified account email; an existing email channel for that address is reused |
| Alert rule | `availability-burn` | Error budget burning more than 14.4× over 1 hour (and 6× over 6 hours) |
| Alert rule | `latency-burn` | The same, on the latency SLO |
| Alert rule | `health-failing` | The health check fails twice in a row |
| Release gate | `availability` | A canary that burns the availability budget faster than 10× is rolled back |

The objectives are deliberately loose: they alert when something is clearly
wrong. Tighten them by editing the objects (see [SLOs](slos.md) and
[Alerts](alerts.md)). An object you edit becomes yours; Gregale leaves it
alone from then on.

The release gate works with the safe-release defaults every app already has:
canary, rollback on first-wake 5xx and the post-release watch.

When the availability alert is on, the platform's own outage email is not
sent for that app, so you are not told twice.

### The health check is not billed

The hourly check runs from the platform, not from your traffic. It adds no
requests or usage to your bill. If it wakes a parked app, that wake is not
billed, and the instance parks again a few seconds after the check instead
of waiting for the idle timeout.

You can pause, resume or delete the check. Its path and interval are fixed.

## Which apps are protected

An app is protected by default when it is created while the capability is
on for your account, and when it is:

- request-mode and public (not `internal`), not a PR preview;
- live in the default environment. Named project environments are not
  covered.

Apps created earlier are not changed. Turn protection on for one with
`gregale app protection on APP`.

## Turn protection on or off

```sh
gregale app protection off shop
gregale app protection on shop
```

Or in `gregale.yaml`, applied on each deploy:

```yaml
protection: off
```

The key takes `on` or `off`. Leaving it out keeps whatever the app has.

`off` deletes the baseline objects that still have the values Gregale set,
alerts and the gate first. It keeps:

- objects you edited;
- an SLO or health check that one of your kept alerts or gates still uses;
- the email channel, which other apps may use.

`on` creates what is missing again, including objects an earlier `off`
removed. Objects you deleted yourself while protection was on stay deleted
until you turn protection off and on.

Both changes are recorded in the audit log (`protection.on`,
`protection.off`). The API equivalent is `PUT /v1/apps/{slug}/protection`
with `{"state": "on"}` or `{"state": "off"}`.
