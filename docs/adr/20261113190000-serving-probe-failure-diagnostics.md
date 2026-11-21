# ADR-20261113190000 · Serving-probe failure diagnostics

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-13
- **Extends:** [operator serving health](20261113120000-operator-serving-probe-health.md).

Serving coverage identifies failing node/URL pairs but retains a generic reason.
A successful retry also hides its first failure from operator reads. Add
diagnostic observations while preserving the two-attempt success policy.

Publish three additional scalar gauges per URL: final failure code, first-attempt
failure code and attempt count. Reuse HTTP status. All fields share the atomic
result textfile and run clock. Fix the numeric code table: 0 none, 1 DNS,
2 connection, 3 TLS, 4 timeout, 5 HTTP status, 6 first-byte latency, 7 body mismatch,
8 unclassified failure. Use typed exceptions and known network errno values;
never interpret arbitrary exception strings. Retain HTTP status once headers
arrive, including when a subsequent read fails.

Normalize four recordings using discovered node-exporter identities, unique
series, matching result scrape timestamps and valid serving coverage. Query
their values and timestamps separately from coverage, under the same deadline
and evaluation time. Four scalars per pair across the 1,000-pair bound fit each
query's 4,096-series limit. Hash URL identities during indexing, keeping
assessment linear in input size.

Add an optional diagnostics object to the API, CLI and generated SDKs. Available
metadata must have complete, current, coherent codes/count/status that agree
with coverage. Retry recovery requires two attempts, a failed first attempt and
a passing final attempt. Missing or invalid observations remain unknown with no
inferred values. Distinguish unavailable evidence from an observed failure whose
category is unknown. Diagnostic availability never becomes a health gate.

Use exact-URL SHA-256 IDs in probe logs and notification messages. Emit fixed
error text rather than exceptions. Raw URLs remain only in private configuration,
state and source metric labels needed for inventory joins. Preserve notification
cadence, failed-submission retries, heartbeat eligibility, authorization and
existing exit semantics.

See the [operator guide](../ops/serving-health.md#inspect-failure-and-retry-diagnostics).
