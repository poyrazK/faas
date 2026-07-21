# ADR-027 · Stripe push observability taxonomy

- **Status:** accepted
- **Date:** 2026-07-21
- **Decision:** Ship a closed 11-label result taxonomy for Stripe push
  failures plus a dedicated `meterd_stripe_push_duration_seconds`
  histogram with buckets `{0.5, 1, 2, 5, 10, 20, 30, 45, 60}` (seconds).
  Both land via a single new method on the existing
  `pkg/wire.OpsMetrics` (`ObserveCode(op, code string, dur)`) and a new
  per-push histogram exposed via `StripePushDuration(result)`.
  Classification lives in `pkg/stripex` as
  `stripex.ClassifyPushError(err error) string` and matches the
  `stripe-go` `ErrorType` constants one-for-one; pre-SDK guard
  failures (no apiKey, negative quantity) match exported sentinels
  via `errors.Is` rather than string fragments.
- **Why:** The §14 M7 push-side acceptance gate needed a
  hand-computable GB-h figure that proves the value handed to the
  SDK equals the meter-side rolled-up number — that was missing. The
  broader finding was that today's only signal for a Stripe push
  failure is a journald `meter: push usage warn` line, and the
  existing `pkg/wire.OpsMetrics.Observe` collapses every error to
  `code="err"`, so the Prometheus dashboard cannot distinguish a
  card-decline burst from a rate-limit storm or a 401 from a
  misconfigured deployment. Operators alerting on "did Stripe pushes
  actually land?" have nothing to alert on.
- **Consequences:**
  - `pkg/stripex/errors.go::ClassifyPushError` is the single seam.
    The label set is closed: `ok`, `no-api-key`, `negative-quantity`,
    `api-connection`, `api-error`, `auth-error`, `permission`,
    `card-error`, `invalid-request`, `rate-limit`, `other`. Adding a
    label requires editing the dashboard; do not extend inline.
  - `pkg/stripex/usage.go` declares two sentinels — `ErrNoAPIKey` and
    `ErrNegativeQuantity` — wrapped via `fmt.Errorf("%w (account %s)",
    ErrNoAPIKey, acct.ID)`. Adding context to the wrapped message
    does not change classification. A future pre-SDK guard introduces
    a new sentinel, not a new string fragment.
  - `pkg/wire.OpsMetrics` gains two seams:
    - `ObserveCode(op, code string, dur time.Duration)` — sister to
      the existing `Observe(op, dur, err)`; the existing call sites
      stay untouched. Existing label contract `code ∈ {"ok", "err"}`
      is preserved.
    - `StripePushDuration(result string) prometheus.Observer` —
      returns the per-push histogram's per-label observer. The
      histogram name is `<prefix>_stripe_push_duration_seconds`; the
      prefix follows ADR-015's per-daemon convention (`meterd_…`).
  - Histogram buckets `{0.5, 1, 2, 5, 10, 20, 30, 45, 60}` cover the
    documented Stripe SLA: p99 ≈ 5 s, p99.9 ≈ 30 s, the 60 s ceiling
    is the documented API timeout. Bucket drift requires a follow-up
    ADR — documented in the histogram `Help` string.
  - At registration time, `pkg/wire.NewOpsMetrics` calls
    `stripePushDur.WithLabelValues(label)` for every label in
    `stripex.PushResultLabels()`. This pre-instantiates the closed
    label set so the histogram's HELP/TYPE and zero-valued buckets
    surface in `/metrics` from the moment the daemon boots — even
    before the first Stripe push. Prometheus' default exposition
    skips HistogramVec series with zero observed label tuples, which
    would render the dashboard's stripe-push panel as "no data"
    until at least one push happened (a real ops hazard). Adding a
    label requires extending `PushResultLabels()` AND this loop; the
    two stay in lockstep via the shared `stripex` package.
  - `pkg/meter/pusher.go::PushHour` calls
    `p.ops.ObserveCode("stripe", code, dur)` and
    `p.ops.StripePushDuration(code).Observe(dur.Seconds())` per
    non-skip SDK call. Skip branches (gb ≤ 0, free plan, suspended,
    missing usage rows) remain silent — only an actual SDK call
    observes, so the dashboard's `pushed-this-hour` semantics stay
    clean for §14.
  - `pkg/meter.NewPusher` grows from 4 args to 5 (added `ops`); the
    new arg is nil-coerced to a fresh test registry inside the
    constructor. Only `pkg/meter/loop.go:78` and tests wire it up.
  - The §14 M7 push-side acceptance test
    (`pkg/meter.TestPushHour_Shadow24h`) is the mirror of the
    existing meter-side `TestInvoiceShadow24h`: 24 PushHour ticks of
    a 256 MB Hobby instance agree with the hand-computed
    `(264 × 60 × 1440) / (1024 × 3600) = 6.187500 GB-h` to within
    0.1 %. The classifier seam has its own focused test
    (`TestPushHour_RecordsStripeError`) that drives a
    `*stripe.Error{Type: ErrorTypeCard}` through the pusher and
    asserts the `meterd_ops_total{op="stripe",code="card-error"}`
    counter increments.

- **Rejected alternatives:**
  - **String-fragment matching for pre-SDK errors** (the pre-ADR
    shape). Brittle: any edit to the wrap message in `usage.go`
    silently breaks classification. Replaced by `errors.Is` against
    `ErrNoAPIKey` / `ErrNegativeQuantity`.
  - **Folding classifier into `pkg/meter`**. `pkg/meter` does not
    import `stripe-go`; pulling `*stripe.Error` knowledge across the
    boundary drags the SDK into a daemon that otherwise doesn't
    touch it. Kept the classifier in `pkg/stripex` (the package that
    already owns the SDK error surface).
  - **Larger bucket set** (`{0.1, 0.25, 0.5, …, 120}`). Higher
    resolution but lower signal-to-noise — the buckets at < 0.5 s
    would mostly be empty because the SDK adds an HTTP round-trip +
    JSON parse overhead that floors at ~50 ms but rarely lands below
    500 ms in practice. The chosen set covers the realistic Stripe
    SLA distribution without bloating the histogram cardinality.
  - **Retry/backoff inside the pusher** (a survey follow-up).
    Stripe's idempotency-key + the local `stripe_push_dedupe` row
    already prevent double-billing; a meterd-side backoff is the
    next survey item and was deliberately out of scope.
  - **Shared `pkg/metertest` for the two `recordingStripe` fakes**.
    The two fakes (`pkg/meter/pusher_shadow_test.go::recordingStripe`
    records full `(acct, hour, gb)` tuples;
    `cmd/meterd/main_test.go::meterRec` only counts calls because
    its assertions are about `/metrics` scrape shape) serve
    genuinely different needs. Lifting either into a shared helper
    would over-fit the other side or grow into a kitchen-sink fake;
    each test reads its fake next to its assertions and stays
    single-purpose. The cmd/meterd fake was renamed from
    `recordingStripe` to `meterRec` to make the divergence obvious
    at a glance.
  - **Renaming `meterd_ops_total{op="stripe",code="ok"}` to a
    different metric name**. The existing per-loop Observe (one tick
    per StripeInterval) keeps that series alive; the new
    `meterd_ops_total{op="stripe",code="<classified>"}` is the
    per-push Observe. Both are real and distinct — keeping them on
    the same counter with different `code` labels avoids a
    dashboard-side JOIN and matches the existing pattern.

## Wire format (canonical)

```
# HELP meterd_stripe_push_duration_seconds Per-push latency to Stripe, labelled by result (ok or stripe-error code).
# TYPE meterd_stripe_push_duration_seconds histogram
meterd_stripe_push_duration_seconds_bucket{result="ok",le="0.5"} 0
meterd_stripe_push_duration_seconds_bucket{result="ok",le="1"} 0
…
meterd_stripe_push_duration_seconds_bucket{result="card-error",le="5"} 1
…

# HELP meterd_ops_total Operations performed, partitioned by op + code.
# TYPE meterd_ops_total counter
meterd_ops_total{op="stripe",code="ok"} N
meterd_ops_total{op="stripe",code="card-error"} M
…
```

`result` and `code` carry the **same** closed label set
(see §"Consequences"). The dashboard-side JOIN is `result == code`
on `op="stripe"` rows.

## Cross-reference

- `pkg/stripex/errors.go::ClassifyPushError` — classifier seam.
- `pkg/stripex/usage.go` — sentinel declarations, wrap site.
- `pkg/stripex/usage_test.go::TestClassifyPushError_*` — 8 cases
  pinning each branch of the classifier.
- `pkg/meter/pusher.go::PushHour` — observation site (the only
  caller of `ClassifyPushError` + `ObserveCode`).
- `pkg/meter/pusher_shadow_test.go::TestPushHour_Shadow24h` —
  §14 M7 push-side acceptance.
- `pkg/meter/pusher_shadow_test.go::TestPushHour_RecordsStripeError` —
  classifier seam integration test (no Postgres required).
- `pkg/wire/metrics.go::ObserveCode` / `StripePushDuration` — the
  new wire seams; mirrors `ADR-015` per-daemon registry convention.
- `cmd/meterd/main_test.go::TestRun_MetricsAddr_StripePushLabels` —
  end-to-end `/metrics` scrape assertion for the new histogram.