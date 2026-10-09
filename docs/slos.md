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

An app can hold up to 10 SLOs. SLOs are available on Hobby and above, like
the per-app metrics they are computed from. SLOs cover the whole app; route
SLOs are a planned follow-up.
