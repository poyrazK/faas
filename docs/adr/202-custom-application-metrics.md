# ADR-202 · Custom application metrics as a scaling signal

- **Status:** accepted
- **Date:** 2026-09-21

## Context

The scaling-signal set is complete for signals the *platform* can observe:
request rate, CPU, in-flight requests, local queue depth, and broker consumer
lag. Every one of them is measured by Gregale about the app.

The signals Gregale cannot see are the ones inside the app's own domain.
"Unprocessed rows in my orders table." "Documents awaiting OCR." "Customers
in the onboarding funnel." These are the numbers an operator actually watches
on a dashboard, and none of them are derivable from request traffic — an app
can be serving zero requests and be catastrophically behind.

ADR-194 left this as the one genuinely unsourced signal, and that remains
true: nothing in the tree carries an app-defined number.

Three delivery mechanisms were considered, and two were rejected on evidence
rather than taste:

- **Prometheus scrape of the guest.** The control plane already runs
  Prometheus with `pkg/promql` and `pkg/appmetrics`, so this looked cheapest.
  But `deploy/ansible/roles/prometheus/templates/prometheus.yml.j2` scrapes
  only platform daemons, never guests, and §11 is explicit that guests get no
  shared host directories and sit behind their own netns with a deny-by-
  default egress policy. Scraping customer guests means a new data path
  across the jail boundary, per-app scrape config generation, and a hole in
  the egress rules. That is a large security change to obtain a number the
  app could simply send.

- **A value carried on the response path.** The guest's only existing channel
  to the host is the ForwardHTTP proxy, so an app could stamp a header that
  vmmd scrapes. It is tiny and needs no new auth — and it is wrong for the
  primary use case. The value would refresh only while requests are being
  served, so an app whose traffic stopped would report a stale backlog and
  scale *down* precisely when its queue was growing. A signal that is
  accurate only when you least need it is not a signal.

- **An authenticated push to the platform API.** Chosen.

## Decision

An app's custom metrics are **pushed** to the control plane:

```
POST /v1/apps/{slug}/metrics
{"name": "orders_pending", "value": 1284}
```

and declared as a scaling target by name:

```yaml
scaling:
  targets:
    - metric: custom
      name: orders_pending
      value: 100        # backlog one instance should carry
```

### Push, because the caller is not necessarily the app

The decisive property is that a **parked app has no process**. A scale-to-
zero platform whose custom-metric signal can only be produced by a running
instance cannot scale from zero on that signal — the one thing a backlog
metric most needs to do.

A push endpoint has no such constraint. The caller may be the app while it is
running, a cron job, a database trigger, or the customer's own infrastructure
watching their own queue. Gregale does not care which, and the app does not
have to be alive for the number to arrive. This is the same reason KEDA's
external scalers exist.

It also costs nothing new in security terms: the endpoint sits on the
existing authenticated API surface, with the app's existing credentials, and
adds no guest-reachable path and no egress-rule change.

### A custom metric is fleet-total, not per-instance

`value` is the total across the fleet that **one instance** should be able to
carry, so `desired = ceil(measured / target)` — the `ClassBacklog` arithmetic
already used by `queue_depth` and `queue_lag`.

Kubernetes exposes both forms (`Value` and `AverageValue`) and makes the
author choose. That choice is exactly the HPA-shaped knob this platform
avoids: the overwhelming majority of custom metrics are backlogs, backlogs
are fleet-total, and a per-instance reading is something the app would have
to divide by a replica count it does not reliably know. Picking one and
documenting it is better than a field most authors would get wrong.

If a genuine per-instance case appears it can arrive as a second metric name
without changing the arbitration contract — the same extensibility ADR-194
was built for.

### A stale metric is no signal

Each stored value carries `observed_at`, and a value older than
`CustomMetricFreshness` reports `Have=false` to the arbiter. This is the same
rule as ADR-198's broker lag and for the same reason: if the pusher stops —
the cron dies, the customer's infrastructure has an outage — the last value
is frozen. Treating a frozen backlog as current would pin the fleet at
whatever it was when the pusher died, indefinitely, and bill for it.

The failure mode is explicitly *scale down*, not *hold*. An app whose pusher
has stopped falls back to its other declared signals, and to `min_instances`.

### Cardinality and plan gating are bounded at write time

`MaxCustomMetricsPerApp` caps distinct names per app. The table is keyed
`(app_id, name)` so a push to an existing name is an upsert and cannot grow
the row count; only a *new* name can, and that is where the cap is enforced.
This matters because the scaling trigger reads these rows on every tick for
every owned app.

The feature is plan-gated. Pushing a metric buys the same scaling capability
`min_instances` and the other targets do, and an unbounded free-tier write
endpoint is an abuse surface.

## Consequences

An app scales on the thing its operator actually watches, and can do so from
zero, which no other signal in the set can offer for an app-domain quantity.

The platform takes on a small write endpoint whose traffic is customer-
controlled. It is bounded by the per-app name cap, by the upsert key, and by
the existing API rate limiting; the row count is `apps × MaxCustomMetricsPerApp`
at absolute worst, which is small next to `instances`.

Gregale does not validate what the number means. A customer who pushes a
counter where a gauge is expected will get monotonic scale-out, and the
runbook says so. This is the same trust boundary as `min_instances`: the
platform enforces limits, not judgement.

Not decided here: metric aggregation across pushers (last-write-wins today),
history or trend-based scaling (only the latest value is kept), and
per-instance custom metrics (see above).
