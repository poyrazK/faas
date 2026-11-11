# ADR 20261111140000 · Analytics delivery evidence and attribution

- **Status:** accepted.
- **Date:** 2026-11-11
- **Amends:** ADR 20261103113952 (request analytics rollups), ADR-745 (custom metric history).

## Context

The analytics investigation found eighteen correctness and availability issues.
Gateway coverage alone did not disclose apid buffer loss or uncommitted rollups.
Current node state could also rewrite a completed historical window. Capped raw
deployment samples were being compared with unsampled traffic totals.

The original evidence, resolutions, and regression tests for AN-001 through
AN-018 are preserved in the [findings ledger](../gregale-analytics-findings.json).

## Decision

Keep the gateway counting pipeline, hourly rollup tier and idempotent gateway
batch commits introduced by the rollup ADR amendments. For legacy raw-row
writers, register a durable pending marker per app/minute and aggregation
batch before acknowledging its first row. Clear it only after all counts and
loss evidence commit. The legacy writer can split rejected batches, so marker
completion follows the successful writes in a separate transaction; a crash
between them leaves conservative pending evidence. Direct store writes can
clear a marker in the count transaction. Failed and crashed writers retain
their evidence. If the buffer fills, persist the affected app/minute as lost
before acknowledging raw telemetry; failure applies transport backpressure.
Restoration losses remain covered by their pending markers and durable gaps.

Preserve raw delivery and gateway counting intervals, plus their distinct app gap histories. Historical
analytics checks evidence overlapping its requested window, independently of
route-removal authorization. Missing historical proof is explicitly unknown.
Coverage records expire after the longest supported telemetry retention.
Each legacy app records when durable ingestion auditing began; earlier windows
remain unknown because old apid losses cannot be reconstructed. Gateway-counted
windows use their independent counting proof instead of raw delivery or legacy
buffer audit markers. This boundary is
receipt time, so replaying an old request cannot retroactively extend the audit.

Deployment traffic and cost shares use deployment-keyed rollups. Optional raw
CPU and dependency enrichments have independent query budgets. Failed sections
are listed in `omitted`; available totals remain readable. Folded `__other__`
groups receive no sampled measures when cohort membership cannot be proved.
Nonstandard HTTP methods share the `OTHER` analytics category; forwarded HTTP
requests retain their original methods. Existing stored analytics methods are
normalized on read as well. SDK enums include this category. Unsupported
methods are acknowledged after aggregation without inserting into the raw
debugger's closed method set; their usage and request-ID journals are unchanged.

Hourly log-drain history expands its effective `from` to include the opening
bucket and returns `requested_from` and `window_aligned`. It reads the last
baseline before that boundary and reports the actual baseline and final
observation times as the effective bounds. Unknown first deltas and gaps are excluded and
disclosed through `complete=false` and `coverage_gaps`, rather than counting
lifetime totals as recent deliveries.

Custom metric admissions, including delta and labelled-series writes, serialize
per app in PostgreSQL, and older samples
cannot replace newer observations. Gross future timestamps are rejected;
accepted small clock skew is clamped to receipt time. Gauges share the regular
push value bound, and replica calculations saturate before integer conversion.

## Consequences

Rollup ingestion adds a small durable write per app/minute/batch. A crashed
writer can leave a window permanently incomplete until retention removes it;
this accurately discloses missing counts. Existing replay and histogram
quantization tradeoffs remain as specified in the rollup ADR. Schema expansion
must precede control-plane rollout. Updated SDKs understand additive omission
fields, unknown coverage, aligned drain windows, and `OTHER` method groups.

## Rollout

Apply both expand migrations before updating apid. Once every old apid writer
has stopped, advance the audit start for existing apps before relying on
completeness. Old writers cannot leave the new pending/lost evidence, so the
mixed-version rollout must remain outside the audited window:

```sql
UPDATE request_analytics_rollup_audit
SET audited_since = greatest(audited_since, clock_timestamp());
```

Run this once after the last old writer has stopped. Apps first observed after
that point establish their audit start automatically. This preserves counts
while marking earlier windows as unknown.
