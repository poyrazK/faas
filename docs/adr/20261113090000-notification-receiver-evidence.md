# ADR-20261113090000 · Receiver transport evidence and local notification drills

- **Status:** accepted for implementation; production rollout pending.
- **Date:** 2026-11-13
- **Extends:** [aggregate operator notification health](20261016130000000-operator-notification-health.md).

Aggregate integration counters cannot establish which configured receiver
completed a request. Enable Alertmanager 0.27's receiver-name metrics and use a
bounded, read-only loopback collector for completed request counts, failures
and latency. Only the fixed production receiver names and reviewed integration
types are exported. No addresses or credentials enter metrics.

Confirm a new successful completed request count across two observations before
advancing a last-success **observation** timestamp. Cold baselines and resets
cannot create receipts. A successful transport request does not establish
per-alert receipt, exact delivery time, external watchdog receipt or human
acknowledgement. Destinations sharing receiver/integration remain aggregated.

Independent deployment intent names expected groups. Recording rules validate
source identity and freshness; missing groups and failed requests warn. The
fleet dashboard preserves unavailable evidence and rejects old recordings.
Existing aggregate API and CLI contracts remain unchanged.

A local drill runs the deployment-pinned Prometheus and Alertmanager with the
production routing tree and isolated webhook transports. It verifies correlated
firing/recovery receipts, receiver failure isolation and retries, and a missed
watchdog deadline after both monitoring processes stop. Timers are compressed;
provider adapters and production external monitor qualification remain outside
this local scope. PR and nightly checks retain a report with explicit scope.

Collector tests cover idle and cold baselines, failures, counter resets and
incoherent reads. Template comparisons cover expected receiver groups. Real
Prometheus tests cover missing, stale, partial and wrong-source evidence,
warning recovery, and dashboard freshness. See the
[operator procedure](../ops/notification-delivery.md) for rollout and evidence
interpretation.
