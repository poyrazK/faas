# ADR-20261113220001 · Serving-probe notification transport evidence

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-13
- **Extends:** [total probe deadlines](20261113210000-serving-probe-attempt-deadlines.md)
  and [probe diagnostics](20261113190000-serving-probe-failure-diagnostics.md).

The compute-node serving probe bypasses Alertmanager when sending its webhook
and external heartbeat. Alertmanager receiver metrics do not cover that path.
Those requests previously had inactivity timeouts and unbounded response reads,
with failures visible only in the journal.

Share the existing disposable Linux worker implementation across probe and
notification requests. Enforce a 10-second total deadline per notification,
including DNS, TLS, response reads and cleanup. Kill and reap workers on
completion, expiry, failed IPC and cancellation. Require HTTPS, reject redirects
and non-2xx responses, and require a complete response body no larger than 4096
bytes. Read at most one extra byte to detect overflow. Emit only fixed safe
failure categories using the existing code table; never return provider text,
credential URLs or response bodies through worker messages or logs.

Export six gauges for each fixed channel, webhook and heartbeat: configured
presence, an attempt in this run, last completed attempt success, its failure
code and completion time, and last successful submission time. Missing history
omits result/time samples. Disabled channels have no historical result samples.
Intentional skips do not advance clocks or reset a known failure. Completed
transport success does not establish provider processing or human receipt.

Keep versioned, owner-readable notification history separate from incident
cadence state. Persist failures independently of whether an incident notice was
delivered. Bind history to an exact credential hash, kept only in this private
file, and discard history on rotation/disablement or invalid data. Preserve
unknown success history instead of inventing epoch-zero receipts. Publish
serving metrics before provider calls and refresh them atomically with
notification outcomes afterward, including when state persistence fails.
Keep the original serving probe-completion timestamp during this second write;
provider latency must not make pre-boundary serving evidence appear newer.

Preserve two probe attempts, notification cadence, undelivered-message retries,
heartbeat eligibility, serving assessments and exit policy. An eligible
heartbeat still runs after a recovery webhook fails. A heartbeat failure is
visible in its own metrics and journal while a passing serving run can exit 0.
Use local fixtures for success, redirects, non-2xx responses, stalled DNS and
HTTP reads, slow streaming, bounded/truncated bodies, history, rotation,
persistence failures, cleanup and redaction. No live messages are required.

See the [operator guide](../ops/serving-probe.md#direct-notification-transport-evidence).
