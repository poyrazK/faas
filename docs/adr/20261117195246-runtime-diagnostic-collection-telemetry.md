# ADR-20261117195246 · Runtime diagnostic collection telemetry

- **Status:** accepted for implementation; production qualification separate.
- **Date:** 2026-11-17
- **Extends:** [advisory runtime build evidence](20261115080000-advisory-runtime-build-evidence.md).

Runtime reports already describe missing, stale and conflicting evidence, but
source errors intentionally exclude private upstream details. Add process-local
Prometheus metrics to the existing protected runtime report collector so operators
can distinguish failed source reads, exhausted deadlines and incomplete evidence.
Register them in apid's existing private operations registry. Instrument only
accepted operator runtime reads; rejected authentication or query validation does
not constitute a collection attempt.

Record total collection duration through preview fitting, source-read durations
and outcomes by fixed diagnostic group, completed collection outcomes, encoded
response bytes and omitted preview items. Outcomes describe collection, not daemon
health, release qualification or HTTP success. Fresh mismatches, not-ready results
and observed stalled loops can constitute complete evidence. Intentional diagnostic
exclusion is not incompleteness. Selected diagnostic summaries and complete core
counts determine incomplete reports; omission alone does not lose summary coverage.

Use only closed group/outcome labels. Never label by role combinations, node,
release, target fingerprint, query text, timestamp, URL or raw error. Query counts
include source-read invocations rejected before transport by cancellation, expiry
or missing configuration. Omission counters count items omitted in each report,
including both inventory preview and response-envelope limits; repeated reads
count again. Response bytes describe the encoded payload with its JSON newline,
not acknowledged delivery. Serialization failure has its own report outcome.

Preserve the existing three-second shared source deadline, source ordering,
selection, full-set summaries, preview priority, 64 KiB envelope and protected
read-only access. Do not add queries, retries, cache reads, probes, health gates,
notification sends or persistence. A nil metrics receiver preserves the original
collector. Existing API schemas and preview SDKs remain compatible.

Verify response/query-count parity, every diagnostic group's attribution,
upstream and invalid-payload failures, deadline and cancellation propagation,
unselected roles, partial evidence, fault/completeness separation, omission and
byte accounting, concurrent observations, closed labels, private detail exclusion,
and registry wiring through the protected API.
