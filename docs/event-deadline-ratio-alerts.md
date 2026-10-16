# Expired-delivery ratio alerts in Go

The Go delivery-health watch can alert on the share of retained pending recipients
with effective delivery-age deadlines that are already expired:

```
ratio = already_expired_count / deadline_recipients
```

The denominator is **all retained captured pending application recipients with
deadlines**, not all pending recipients, observations, attempted deliveries or
completed events. The numerator and denominator come from the same complete
read-only deadline-window snapshot. This is a backlog ratio, not a delivery success
SLO or a historical expiry rate.

```go
recovery := int64(500) // 5%, inclusive
options := faas.EventDeliveryHealthWatchOptions{
    Window: "5m",
    Interval: 5 * time.Second,
    Timeout: 10 * time.Minute,
    MaxAge: 30 * time.Second,
    RateRisk: &faas.EventDeliveryRateRiskOptions{
        DeadlineWindow: &faas.EventDeliveryDeadlineWindowOptions{
            Within: 5 * time.Minute,
            ExpiredRatio: &faas.EventDeliveryDeadlineRatioOptions{
                WarningBasisPoints: 1000, // 10%, inclusive
                RecoveryBasisPoints: &recovery,
                MinimumRecipients: 100,
            },
        },
        Sustain: time.Minute,
        RecoverySustain: 2 * time.Minute,
    },
}
```

At least 100 pending deadline-bearing recipients must be observed in every eligible
sample. The condition opens at >=10% for one minute and resolves at <=5% for two
minutes, with at least two fresh advancing observations for each transition. Between
5% and 10%, an established incident stays firing. A resumed active incident requires
fresh warning or recovery proof; middle-band observations do not establish either.

## Validation and exact boundaries

`ExpiredRatio` is optional and can be enabled without any count thresholds. It can
also run alongside total and reason-specific count alerts, independently of them.
`WarningBasisPoints` must be an integer 1..10000 (100 basis points = 1%; 10000 = 100%).
`RecoveryBasisPoints` must be nonnegative and strictly below warning. Nil defaults
to warning minus one; a pointer to zero explicitly requires a zero numerator in a
qualifying cohort. `MinimumRecipients` is required and accepts 1..9007199254740991.
There is no implicit minimum-cohort default. Option pointers are copied before
validation and polling; do not mutate options concurrently with the initial call.

Existing shared `RateRisk.Sustain` / `RecoverySustain` defaults and 100ms..24h bounds
apply. A valid whole-millisecond `DeadlineWindow.Within` is still required because
the condition uses the existing endpoint and binds to its horizon. The ratio counts
all expired deadlines and all pending deadline-bearing recipients independently of
that horizon. `Window` remains the broader delivery-health observation window.

The metric identity is
`deadline_window.expired_ratio_basis_points.<millis>.minimum_recipients.<minimum>`.
Condition/alert `Value`, `Threshold` and `RecoveryThreshold` use basis points. Values
can be fractional; thresholds are integers. The scale constant
`EventDeliveryDeadlineRatioBasisPointsMax` represents 100%.

Count multiplication uses a 128-bit product and exact quotient/remainder. Nonzero
remainders remain strictly between their adjacent integer basis-point boundaries
when represented as float64. Thus warning and recovery comparisons against integer
thresholds cannot change because of multiplication overflow, floor rounding,
float rounding or JSON round-trip rounding, including at the maximum supported
count. Displayed values are approximate: do not round them to decide whether an
incident should resolve. Raw numerator/denominator remain available in
`RateEvidence.DeadlineWindow.Summary`.

## Insufficient and missing evidence

If the denominator is zero or below `MinimumRecipients`, the condition stays
unknown and resets confirmation. Progress explains
`insufficient_deadline_recipients`. The existing Go lifecycle closes an active
incident with `confirmation_lost`, never a successful `resolved` transition. It
clears that incident identity; a later qualifying breach requires new sustained
warning proof and creates a new incident. A backlog that drains below the minimum
therefore cannot produce a successful ratio recovery notification. Pair an absolute
expired-count condition with the ratio when zero remaining expired work must be
tracked separately.

Incomplete counts, missing/unknown reason keys, invalid scope/control metadata,
failed/unsupported reads, mismatched horizons and stale/future/repeated/regressing
observations also remain unknown. Gaps reset confirmation; downtime is not proof.
The existing exact full partitions are required even though this ratio uses totals.
Other eligible count conditions continue independently when only the ratio's cohort
is too small. There is no fallback to sampled counts or successful recovery on
missing evidence.

## Checkpoints and operational limits

The condition reuses version 2 Go delivery-health checkpoints, incident identities,
durable alert delivery, acknowledgment handling and reserved closing queue slots.
Existing count identities/order are preserved; an enabled ratio condition is appended.
Resume binds to the same horizon, minimum cohort, warning/recovery basis points and
durations. Omitting recovery and explicitly supplying its default are equivalent.
Historical samples and timer progress never become fresh proof. Existing configurations
without this option keep their request behavior and checkpoint policy.

Older clients reject the new metric; upgrade the resuming client before loading a
checkpoint containing it. The CLI exposes these options as documented below;
Node watches also support exact ratio conditions with version 2 Node checkpoints;
Python watches support them with exact integer comparisons and version 2 Python checkpoints too. No server endpoints,
migrations or notification destinations are added. The existing complete
window read supplies all count and ratio conditions, so no extra request is added
per ratio. Existing read budgets and unknown-on-timeout behavior apply.

A ratio can improve when new deadline-bearing work increases the denominator,
expired rows leave retention, or deadlines acquire overrides; it can worsen as
nonexpired work completes. Resolution does not prove expired deliveries recovered
or business processing succeeded. Count and ratio incidents can overlap for the
same work and remain independent.

## CLI ratio watch

```sh
gregale events subscription-delivery-health APP SUB --watch \
  --deadline-window 5m \
  --deadline-expired-ratio-threshold 10 \
  --deadline-expired-ratio-recovery 5 \
  --deadline-expired-ratio-min-recipients 100 \
  --deadline-sustain 1m --deadline-recovery-sustain 2m \
  --interval 5s --timeout 10m --confirmation-progress \
  --checkpoint ./delivery-health.json --alerts-json
```

Percent arguments are decimal numbers from 0 through 100, with at most two digits
after the decimal point. `10` means 10%, not 10 basis points. `0.01` is the smallest
positive warning. Use `0.5`, not `.5`; exponent notation, signs, percent suffixes,
trailing decimal points, nonfinite values and extra fractional digits are rejected.
Conversion to integer basis points uses decimal arithmetic, never float rounding.
Repeated ratio flags are rejected.

A zero warning disables the ratio condition; omitted warning also leaves it disabled.
Recovery and minimum flags require a positive warning. An enabled ratio always
requires `--deadline-expired-ratio-min-recipients` in 1..9007199254740991; there is no
implicit minimum. Recovery must be strictly below warning. Omission defaults to
warning minus **0.01 percentage points**, while explicit zero requires no expired
work in a qualifying cohort. For example, warning `10` without recovery uses 9.99%.

These settings require `--watch`, a valid `--deadline-window` and at least one
positive count or ratio warning. The existing `--deadline-sustain` and
`--deadline-recovery-sustain` durations are shared by all selected count and ratio
conditions. Ratio-only watches are supported. Invalid combinations fail before
authentication, polling or checkpoint preparation. Absolute count/reason flags
continue to work alongside the ratio.

Final text output shows state, warning/recovery percentages, minimum cohort and,
when complete eligible evidence is available, the observed percentage, expired
numerator, deadline-bearing denominator and observation timestamp. Approximate
percentages are prefixed with `~`; rounding display values never controls alerts.
Zero denominators display an undefined ratio rather than 0%. Low cohorts explain
that confirmation is unknown. Missing/incomplete evidence displays unavailable
rather than inventing a percentage or cohort. Confirmation diagnostics distinguish
stale/repeated observations, gaps, pending sustain and notification blockage.
`--confirmation-progress` prints these details after every observation too.

Text lifecycle alerts on stderr also label ratios as percentages. `--alerts-json`
keeps the existing alert JSON schema and **basis-point units**, and final `--json`
keeps the existing result with basis-point conditions plus raw window counts.
Do not combine live text progress with `--json`.

Use the existing checkpoint/resume/acknowledgment options. Resume requires the same
effective horizon, minimum cohort, warning/recovery percentages and sustain policy;
decimal forms such as `5`, `5.0` and `5.00` have the same effective value. Persist
before notification and deduplicate replayed alerts as usual. Low cohorts and lost
evidence retain the Go `confirmation_lost` lifecycle described above; they never
produce a successful ratio recovery by themselves. The CLI adds no read or delivery
action beyond the existing Go watch.

## Node ratio watch

Node's `expiredRatio` option selects the same backlog numerator/denominator and
integer basis-point policies. It uses exact BigInt comparisons, JSON-safe source
samples and its own version 2 checkpoint format. See
[Node ratio configuration and compatibility](event-deadline-node-watch.md#expired-backlog-ratios).

## Python ratio watch

Python's keyword-only `expired_ratio` option selects the same backlog ratio with
integer basis-point boundaries and an explicit minimum cohort. It retains source
samples, uses exact integer comparisons and version 2 Python checkpoints. See
[async Python ratio configuration and compatibility](event-deadline-python-watch.md#expired-backlog-ratio-conditions).
