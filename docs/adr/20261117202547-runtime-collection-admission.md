# ADR-20261117202547 · Runtime collection admission

- **Status:** accepted for implementation; production qualification separate.
- **Date:** 2026-11-17
- **Extends:** [runtime diagnostic collection telemetry](20261117195246-runtime-diagnostic-collection-telemetry.md).

Rate limits govern arrivals but do not bound overlapping runtime diagnostic
collections and their Prometheus fan-out. Admit at most two source-backed
`runtime-builds` collections per apid process. This conservative initial bound
limits overlapping fan-out; it is not a measured throughput claim, tenant quota
or fleet-wide semaphore. Declare the cap and one-second retry hint in
`pkg/api/limits.go`.

Use one zero-value-ready admission object owned by the server. Acquire capacity
after operator authentication, scope/MFA/allowlist enforcement and query
validation, before registry or Prometheus reads. There is no waiting queue,
request coalescing, cached result or cross-request evidence reuse. At saturation,
return HTTP 429 `obs_runtime_busy` with `Retry-After: 1`, limit/observed metadata,
documentation and `Cache-Control: no-store`. The hint reserves no capacity and
does not trigger an internal retry. The existing documented 429 problem response
and string problem-code schema remain compatible.

Unselected roles retain their source-free unknown response without consuming a
slot. Already-canceled requests produce canceled advisory evidence without
accessing inventory or Prometheus. Once admitted, retain the slot until all
collection work, aggregation and preview fitting unwind, even after cancellation.
Release on every return. Preserve the existing three-second shared source budget,
source priority, diagnostic selection, full-set coverage and response limits.
Slow response transmission does not hold collection capacity.

Admission remains active with metrics disabled and survives registry rebinding.
Expose `apid_operator_runtime_active_collections` as a gauge of the admission
object, and `apid_operator_runtime_refused_collections_total` as a counter of
capacity refusals. Neither has labels. Refusals do not count as completed reports
or source attempts. Authentication, validation, unselected roles and preflight
cancellation do not increment capacity refusals.

The CLI records a 429 with `X-Faas-Error-Code: obs_runtime_busy` as
`source_runtime_busy`, source HTTP status 429 and advisory unknown. Other 429
responses retain the generic `source_rate_limited` reason. Preserve the three
qualification gates, fault flags, exit codes and polling budget. Do not add an
immediate retry or legacy fallback.
Verify simultaneous admission, exact refusal accounting, no source access for
refused work, cancellation while unwinding, capacity reuse, success/failure
cleanup, cheap reads at saturation, metric independence/rebinding, protected HTTP
ordering and saved CLI advisory behavior.
