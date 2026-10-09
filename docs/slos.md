# SLOs and error budgets

An SLO (service level objective) states how reliable an app should be — for
example "99.9% of requests succeed over 30 days" or "99.5% of requests finish
within 250 ms over 7 days". The gap between the objective and 100% is the
error budget: the failures you can afford before the objective is missed.
Gregale tracks each SLO's budget so you can see how much risk is left before
a release (ADR-747).

Every app also has the fixed 99.5% availability panel from `gregale slo`
(ADR-082). Use your own SLOs when a different target, a latency goal, or a
30-day budget matters.

## Define an SLO

```sh
gregale slos create --app shop --name checkout --objective 99.9
gregale slos create --app shop --name fast-pages --sli latency \
  --latency-threshold-ms 250 --objective 99.5 --window-days 7
gregale slos list --app shop
gregale slos rm --app shop SLO_ID
```

The API equivalent is `POST /v1/apps/{slug}/slos`; `GET`, `GET .../{id}` and
`DELETE .../{id}` read and remove definitions.

| Field | Values |
|---|---|
| `sli` | `availability`: share of non-5xx responses among 2xx and 5xx responses (4xx are the client's error and do not count). `latency`: share of requests that finish within the threshold. |
| `latency_threshold_ms` | Latency SLI only: 5, 10, 25, 50, 100, 250, 500, 1000, 2000, 5000 or 10000. These are the gateway's histogram buckets, so attainment is counted exactly rather than estimated. |
| `objective_pct` | 90 to 99.99, at most two decimals. |
| `window_days` | 7 or 30; the budget is measured over this rolling window. |

## Check the error budget

```sh
gregale slos status --app shop SLO_ID
```

```
checkout — 99.9% available over 30 days
  Attained:          99.950% (2398800 of 2400000 requests)
  Budget remaining:  50.000%
  Burn rate:         0.80× (1h) · 1.10× (6h)
  History:           720 of 720 hours since 2026-09-09T12:00:00Z
```

- **Budget remaining** is 100% when nothing failed, 0% when failures have used
  the whole budget, and negative once the objective is missed for the window.
- **Burn rate** compares the recent failure rate with the rate that would use
  exactly the whole budget over the window: 1× is on pace, 14.4× spends a
  30-day budget in about two days. Burn rates are live; the budget moves in
  whole hours.
- **History** counts completed hours Gregale has recorded. A new SLO's window
  starts at the hour it was created and grows to the full window. Hours missed
  during a metrics outage are recovered automatically for up to 14 days;
  fewer recorded than expected hours means a gap older than that, which is
  left out rather than guessed.

`GET /v1/apps/{slug}/slos/{id}` returns the same figures in `status`.

## Alert on an SLO

Two alert metrics watch one SLO, named with `slo_id` (`--slo` in the CLI):

```sh
# Page when the budget is burning fast: both the last hour at 14.4x and the
# last six hours at 6x the sustainable rate.
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "checkout burning" \
  --metric slo_budget_burn --slo SLO_ID --comparison gt --threshold 14.4 --window-spec 1h \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin

# Warn when less than a quarter of the window's budget is left.
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "checkout budget low" \
  --metric slo_budget_remaining_pct --slo SLO_ID --comparison lt --threshold 25 --window-spec 1h \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

- `slo_budget_burn` is the 1-hour burn rate, with the 6-hour rate scaled onto
  the same threshold, so `gt 14.4` fires only when both windows burn fast. A
  short spike that has already passed, or a slow leak, stays below it. This is
  the same shape as the fixed `slo_burn_rate` alert, measured against your
  own objective.
- `slo_budget_remaining_pct` is the budget-left figure from `gregale slos
  status`. With no traffic in the window the rule is `unknown` rather than
  reading as 100%.
- The rule's `window_spec` is required but ignored: the SLO defines the
  windows. Deleting the SLO deletes its alert rules.

The dashboard's app page lists every SLO with its budget left and current
burn rate under **Your SLOs**.

An app can hold up to 10 SLOs. SLOs are available on Hobby and above, like
the per-app metrics they are computed from. SLOs cover the whole app; route
SLOs are a planned follow-up.
