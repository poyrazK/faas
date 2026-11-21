# ADR-20261113233000 · Operator evidence for direct probe notifications

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-13
- **Extends:** [notification warning rules](20261113230001-serving-probe-notification-alerts.md)
  and [operator serving health](20261113120000-operator-serving-probe-health.md).

Add optional `probe_notifications` to the existing operator serving-health
response, CLI and generated SDKs. Preserve authorization, read-only behavior,
coverage assessment, exit policy and deployment qualification. The response
describes completed direct webhook/heartbeat submissions without executing a
probe, sending a message or reading destination credentials.

Assess two channels per unique expected node independently of serving results.
Read normalized state plus a run clock, outcome plus code, and completion plus
success clocks in three value/timestamp query pairs. All twelve serving-health
queries share one pinned evaluation time and the existing three-second budget.
At 1,000 nodes, each vector contains at most 4,000 series, within the 4,096 limit.
Validate identities, finite values, timestamps, code domains, source age and
cross-field coherence. Unknown observations preserve nulls; historical success
does not become current provider availability or a receipt. Keep submillisecond
ordering when comparing completion and success clocks.

Retain counts for every expected channel and a failure-first preview of at most
32 channels. Disclose expected count, limit, omissions and truncation. Order
ties by node/channel and validate counts, preview priority and identities before
CLI output. This bounds the extra data in existing deployment reports rather
than repeating node-wide evidence on every URL. Verify the maximum fleet and
the populated report remain inside their existing transport envelopes.

Use fixed states and reasons, stable node/channel names and reviewed failure
codes. Exclude URLs, addresses, arbitrary labels, provider text and response
bodies. Older servers may omit the optional section. The standalone CLI rejects
malformed optional metadata before human or JSON rendering. Deployment reports
retain safe optional observations but replace malformed data with explicit
unknown evidence; optional history cannot become an extra qualification gate.
Keep the existing three checks, timeline meaning and fault-history criteria.

Test fresh, unknown, missing, stale, future, duplicate, foreign and contradictory
observations, query failures, cancellation, maximum vectors and preview bounds,
SDK round trips and older-server omission. Bind the exact additional queries to
real Prometheus semantic fixtures. Production convergence is separate.
