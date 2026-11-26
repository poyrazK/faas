# Cost controls

Every paid plan has a hard spend cap from the day you subscribe. Usage past
your plan's allowance is billed up to the cap and never past it. When the
month's overage reaches the cap, your workloads stop and queued work waits
until you raise the cap or the month ends. Nothing is deleted.

This page shows how to set the cap, get alerts before you reach it, stop
preview environments at a limit, keep each app's compute in bounds, and see
where the money goes. [Billing](billing.md) is the full reference;
[plans and pricing](plans.md) lists every price and limit.

## What you pay for

A month's bill is the plan price plus overage, before tax:

- **Compute** is the RAM you give an app times the time its instances run,
  in GB-RAM-hours. Each plan includes some; past it, paid plans pay
  $0.05 per GB-RAM-hour. CPU is not billed separately.
- **Egress** past the plan's allowance is billed only if you turn on
  [egress overage](#choose-what-happens-at-the-egress-allowance), at
  $0.03 per GiB.
- **Managed PostgreSQL** past the plan's allowance is billed only if you turn
  on its overage; see [managed PostgreSQL](#managed-postgresql-and-object-storage).
- **Object storage** past the plan's included storage costs $0.03 per
  GiB-month. Requests and bucket egress are not charged.

Everything in that list counts toward the spend cap. These cost nothing:

- an app parked after its idle timeout, and an instance
  [paused between requests](cold-wake.md#paused-between-requests);
- requests the edge refuses, such as [edge rules](edge-rules.md) for rate
  limits, IP, country and token checks: they are answered at the edge and
  your app never wakes for them;
- builds, up to the plan's monthly build minutes. Past them a build is
  refused, not billed;
- features: canary rollouts, PR previews, custom domains, logs, realtime and
  the rest are not billed. Only the compute they run on is.

The Free plan never bills. It stops at its allowance (20 GB-RAM-hours and
10 GiB of egress a month) and cannot turn on overage.

## A good setup in five minutes

1. Check your spend cap: `gregale billing spend-cap`.
2. [Create a budget](#get-alerts-before-you-reach-a-limit) with an alert
   below the cap, and send it to your Slack or PagerDuty channel.
3. If you use PR previews, [stop them at a limit](#stop-preview-environments-at-a-limit).
4. Give each app only the RAM it needs, and a sensible
   [maximum instance count](#keep-each-apps-compute-in-bounds).
5. Look at [per-app costs](#see-where-the-money-goes) once a month.

## Set your spend cap

The spend cap is a hard ceiling on the overage billed in a UTC calendar
month. Your plan's included GB-RAM-hours don't count against it. A paid plan
always has one, and starts with one more allowance's worth of overage:

| Plan | Included GB-RAM-hours | Default cap | Highest cap |
|---|---:|---:|---:|
| Hobby | 200 | $10 | $75 |
| Pro | 1,000 | $50 | $200 |
| Scale | 3,000 | $150 | $1,000 |

So on Pro with the default cap, a month costs at most $80 before tax: the
$30 plan plus $50 of overage. Need a cap above the highest one? Ask support.

The amount is in cents:

```sh
gregale billing spend-cap          # show the current cap
gregale billing spend-cap 2500     # cap overage at $25.00 a month
```

`POST /v1/account/overage-cap` with `{"overage_cap_cents": 2500}` does the
same. An organization that owns its resources has its own cap, which its
owner sets with `POST /v1/orgs/{slug}/overage-cap`.

Changing the cap needs an `admin`-scoped credential and multi-factor
authentication; in the dashboard it also needs a recent step-up. A deploy key
in CI, or an agent connected over MCP, can deploy but cannot raise the cap.

### What happens at the cap

When the month's overage reaches the cap:

- usage is billed exactly up to the cap. Raising the cap later does not bill
  the usage it excluded;
- running instances, minimum-instance floors, service replicas, workers and
  warm pools stop within one scheduler tick;
- new requests that would wake an app get HTTP 402 `admission_refused`, with
  the month's overage and the cap in the response;
- crons, delayed tasks, webhooks, async invocations and event deliveries wait
  instead of failing, and keep every attempt they have left. Queued job tasks
  stay queued. A cron that misses several runs runs once when the cap lifts;
- managed PostgreSQL databases stop too, and keep their data. Object storage
  keeps serving within its plan limits;
- you get an email with the cap and the month's overage.

Raise the cap, or wait for the next month, and workloads wake again on the
next request or scheduler tick. You don't need to redeploy.

### Stop all spending now

A cap of 0 counts as reached at once, so every workload stops, even within
the included allowance. Use it as a pause switch; nothing is deleted.

```sh
gregale billing spend-cap 0        # stop everything
gregale billing spend-cap 5000     # start again with a $50.00 cap
```

## Choose what happens at the egress allowance

Each plan includes app egress every month: Free 10 GiB, Hobby 100 GiB,
Pro 500 GiB, Scale 2,000 GiB. It counts what your apps send, including
responses from the edge cache. By default every plan stops there: your apps
park until the month ends or the plan grows, while databases and buckets keep
running. Gregale emails you once a day from 80% of the allowance.

A paid account can choose to pay for egress past the allowance instead:

```sh
gregale billing egress-overage          # show the current choice
gregale billing egress-overage on       # $0.03 per GiB past the allowance, within the spend cap
gregale billing egress-overage off      # stop at the allowance again
```

Only egress sent while it is on is billed. Egress from before you turned it
on never is. See [egress overage](billing.md#egress-overage).

## Get alerts before you reach a limit

A budget watches spending and alerts you at the amounts you choose. It never
changes your bill. A budget can cover your account, a project, an
environment, an app or a job.

The **Usage** page in the dashboard creates account and app budgets. From the
CLI, save a spec to `budget.json`:

```json
{
  "name": "Monthly compute",
  "scope": {"kind": "account"},
  "currency": "USD",
  "meters": ["compute"],
  "basis": "net_usage",
  "limit_millicents": 1000000,
  "notify_millicents": [800000],
  "mode": "monitored",
  "action": "notify",
  "drain_seconds": 0,
  "resume_rule": "next_period",
  "enabled": true
}
```

Amounts are in millicents (1,000 per cent), so this budget alerts at $8 and
again at its $10 limit. Then create it:

```sh
gregale billing budgets create --file budget.json --key monthly-compute
gregale billing budgets list
```

To watch one app, project, environment or job instead, set `scope` to
`{"kind": "app", "id": "APP_ID"}` (or `project`, `environment`, `job`) with
the resource's ID. `meters` can also include `egress`, and `object_storage`
on an account budget.

### Send alerts to Slack, PagerDuty or email

Budget alerts always go to the account email. They can also go to up to five
of your [notification channels](alerts.md#notification-channels):

```sh
gregale alert channel list                              # find channel IDs
gregale billing budgets channels --set CHANNEL_ID POLICY_ID
```

On PagerDuty, every threshold of one budget in one month updates a single
incident.

### Projected overruns

Gregale also projects each budget to the end of the month at this month's
rate. When the projection reaches the limit while spending is still below
it, you get one "projected to exceed its limit" alert for the month. It
needs a day of usage first.

```sh
gregale billing budgets get POLICY_ID    # spending so far, projected month end, alerts sent
```

## Stop preview environments at a limit

A budget can stop preview environments when they reach a limit, while
production keeps serving. This spec stops previews once the account's
compute reaches $10.00, after letting them drain for 30 seconds:

```json
{
  "name": "Preview spending",
  "scope": {"kind": "account"},
  "currency": "USD",
  "meters": ["compute"],
  "basis": "net_usage",
  "limit_millicents": 1000000,
  "notify_millicents": [800000],
  "mode": "monitored",
  "action": "stop_previews",
  "drain_seconds": 30,
  "resume_rule": "manual",
  "enabled": true
}
```

Check what it would select before you save it. The preview writes nothing:

```sh
gregale billing budget-preview --file previews.json
gregale billing budgets create --file previews.json --key preview-spending
```

`drain_seconds` can be 0 to 300. With `resume_rule` set to `next_period`,
previews may wake again at the start of the next month; with `manual`, they
stay stopped until you edit, disable or delete the budget. Usage can pass the
limit a little while previews drain. See
[monitored preview stopping](billing.md#monitored-preview-stopping).

## Keep each app's compute in bounds

An app's compute cost is its RAM times the time its instances run. These
settings decide both:

```sh
gregale app my-api --ram 512              # RAM per instance, in MB
gregale app my-api --max-concurrency 3    # most instances the app runs at once
gregale app my-api --min 0                # no warm floor: park when idle
```

- **RAM** is the multiplier on every running second. Start small and raise it
  if the app runs out of memory.
- **Maximum instances.** One instance takes up to 80 requests at once before
  Gregale starts another, up to this ceiling (and never past your plan's:
  1, 2, 5 or 20). Past it, requests queue instead of starting more instances;
  see [scaling policy](scaling-policy.md).
- **Minimum instances** (Pro and Scale) keep instances warm, and are billed
  like the instances they keep running. They stop at the spend cap too.

CPU follows RAM (one vCPU per GB, at least one) and is not billed, so
`--cpu-millicores` doesn't change your bill.

## Managed PostgreSQL and object storage

**Managed PostgreSQL** stops at the plan's monthly allowance unless you turn
on overage. A database's compute suspends when it is idle, after 2 minutes by
default.

```sh
gregale postgres usage                          # this month's usage against the allowance
gregale postgres overage on                     # keep running past the allowance, up to twice it
gregale postgres suspend-timeout set orders-db 5m
```

With overage on, what passes the allowance is billed and counts toward the
spend cap. See [usage and limits](postgres.md#usage-and-limits).

**Object storage** bills only the bytes you store, averaged over the month.
Storage past the included amount costs $0.03 per GiB-month up to the plan's
ceiling, and a lower spend cap lowers that ceiling: with a cap of 0, storage
stops at the included amount. `gregale bucket usage` shows where you are.
See [object storage limits](object-storage.md#limits).

## See where the money goes

```sh
gregale billing usage                   # this month's usage
gregale billing forecast                # this month's bill at the current rate
gregale billing costs --month 2026-10   # usage costs by app, job, project and environment
gregale app costs my-api                # one app's compute and egress costs
```

- The **forecast** needs one full day of usage in the month.
- **App costs** (paid plans) show a six-month trend on the app's dashboard
  page, with CSV downloads, and the **App costs** page ranks your apps by
  cost.
- **FOCUS export** (beta): `gregale billing export --month 2026-10 --out invoices.zip`
  exports invoices as a FOCUS 1.4 dataset for FinOps tools.

Cost reports cover compute and app egress. Subscription fees, tax and credits
are on your invoices in the billing portal (`gregale billing portal open`).

## Plan changes and failed payments

- A downgrade you ask for is refused until your apps, databases and stored
  objects fit the smaller plan, so it never parks an app by surprise.
- If a payment fails, your apps, databases and storage keep running and new
  deploys wait. Seven days later an unpaid account moves to the Free plan.
  Nothing is suspended or deleted because of the failed payment. See
  [failed payments](billing.md#failed-payments).

## Related

- [Billing](billing.md): the full reference for caps, budgets, costs and
  invoices.
- [Plans and pricing](plans.md): every price, allowance and limit.
- [Scale to zero](cold-wake.md): parking, pausing and waking.
- [Edge rules](edge-rules.md): refuse traffic before it reaches your app.
