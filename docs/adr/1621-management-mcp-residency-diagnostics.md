# ADR-1621: Observed residency diagnostics through management MCP

## Status

Accepted. Extends ADR-1618, ADR-1619 and ADR-1620.

## Decision

Expose read-only `gregale_get_running_causes` through the existing native
`GetAppDebugRunningWithLimit` client. Accept app, optional positive lookback
duration/day alias, and a history limit (default 10, maximum 20). Centralize
these transport bounds and a 16-cause projection cap in `pkg/api/limits.go`.
Native ownership, debugger plan gating, 24-hour default and retention remain
authoritative. Return native errors without retrying or broadening the window.

Reuse the independently selected app-wide diagnostic consent, pinned to the
reviewed app ID, across environments. Update browser review to name request,
performance and scheduler residency evidence explicitly. The internal encrypted
`request_read_apps` grant remains unchanged; absent entries deny remote reads.
Local stdio uses native credential authority. No environment, log or write
permission is added. Validate native app/window metadata and recheck app identity
and consent immediately before output. Validate each distinct attributed
deployment's app/scope before projection and again before release, with the app
identity check last.

Project explicit DTOs for configuration, scheduler causes and bounded retained
history. Preserve observed timestamps, degraded flags, native history truncation,
per-observation cause truncation and credential-redacted text truncation. Exclude
flow topology, endpoint addresses/ports, event IDs and trace/wake/instance IDs.
Text uses the existing 256-byte field budget after known-credential redaction.
Validate counts, timestamps, history size and the association of the native
current observation with retained history. Empty history stays explicitly empty.

The native `current` field checks that a resident instance exists, then reuses
the newest retained observation. It is not a live blocker measurement. Always
report freshness as unknown and expose observation age relative to native
`window_end`, without an invented freshness threshold. Label configuration's
basis: native app configuration when no current observation is returned, or the
latest retained observation while resident. Keep historical settings separate.
An absent cause or `no_blocker_observed` proves neither inactivity nor parking.
Workload residency does not establish queue processing or readiness.

Request attribution is a nearest retained telemetry aggregate. Expose its safe
lookup identifier, route without query/fragment, method, deployment/scope,
timestamp, represented count and match delta, explicitly labeled as approximate
aggregate attribution rather than exact individual causation. Do not export
trace, wake or instance identities. Agents may drill into this representative
using the existing exact request tools; independent reads are not an atomic
snapshot.

Add `gregale_residency_diagnosis` with app and optional lookback. It guides agents
through the observed causes, configuration basis, age, missing/degraded evidence
and optional request drill-down. Retrieving the prompt fetches no scheduler or
request evidence. Completion enumerates only currently consented app identities;
lookback values and private evidence are not enumerated.

## Consequences

Agents can explain retained evidence for residency while preserving uncertainty.
No native API, migration, telemetry collection, probe, replay, model request,
hypothetical savings calculation or configuration mutation is introduced.
Application text remains private and untrusted; known-credential redaction is
not a general arbitrary-secret detector. Portable fixtures and pinned real Codex
acceptance cover consent, redaction, older/empty/degraded observations and OAuth
restart/refresh/revocation. Native/provider deployment qualification is separate.
