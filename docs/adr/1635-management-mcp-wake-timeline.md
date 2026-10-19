# ADR-1635: Exact wake-stage inspection through management MCP

## Status

Accepted. Extends ADR-1634 wake history and ADR-1618 app-wide diagnostic consent
using the existing typed ListWakeTimeline client. No native endpoint, SQL,
persistence, migration, VM lifecycle or deployment change.

## Decision

Add read-only gregale_get_wake_timeline with app, exact unchanged wake_id,
optional since and native event limit. Reuse native ownership, Hobby+ plan,
MFA/read-surface authorization and diagnostic consent across environments. Pin
and recheck immutable app identity before returning. Make one native call;
never enrich history, requests or prompts automatically. Retained instance
history and events are independent; return native missing/plan errors without
fallback. No on_behalf_of or unsupported query fields are accepted.

Centralize defaults/maxima 50/100 events, timestamp bound 64 bytes and the JSON
exact integer maximum in pkg/api/limits.go. Omitted since has no time cutoff;
explicit nonzero RFC3339 since is normalized to UTC/native PostgreSQL microseconds.
The actual source bound is exclusive (at > since), despite stale inclusive SDK
comments. Expose submitted selection and not_echoed_by_native window verification.
There is no until, effective-window, as_of or source completeness metadata.

Expose one oldest-first native response and its continuation hint, with
pagination_status timestamp_only_not_lossless and no next_arguments/cursor input.
The native store orders by at/id but the cursor contains only at; exclusive
continuation can skip tied rows. Forge filtering happens after the source limit,
so the hint can also precede the last visible event. Do not reinterpret it as an
event seek tuple, auto-page or advance by epsilon. Preserve native bounds, event
count, ties, repeated kinds and unknown capture/freshness. No hint is not proof
of exhaustive capture. Native canonical boot-marker selection remains authoritative.

Explicit DTOs project recognized queue/admission/boot/readiness/proxy/parking
kinds, selected producer timestamps, admission counts/capacity, readiness-loop
duration/probes, acceptance-to-first-byte and separate post-wake proxy duration,
and native restore/cold-boot call total/wait-ready summaries. Expose distinct
timing bases; recorded event time is not necessarily a phase boundary. Never
derive a queue wait or sum/subtract overlapping timings into a critical path.
Multiple events can concern different requests/instances sharing a wake.

Validate matching response and per-event app/wake IDs, native echoed limit,
event count/order, exclusive lower bound and bounded timestamps/hints. Validate
selected payload types and finite nonnegative exact whole measurements. Selected
raw keys preserve observed numeric zero and false booleans; missing/null remains
unknown. Unlike flattened wake history, these native payloads retain presence.
Both queued_count and concurrency_at_admit use the admission concurrency ledger,
not gateway waiting queue length. Missing/zero producer timestamps remain absent.

Redact then bound method/tier/trigger display labels and report modifications.
Expose only the closed after_restore_failed fallback reason; unknown reasons
remain unrecognized without free text. Unknown event kinds retain timestamp and
an unrecognized_event marker without interpreting payloads. Stage observations
count this page only; missing stages, filtered emptiness or unknown details
cannot establish health, failure, current state or a complete event narrative.

Exclude raw payloads, actor labels, request/instance/node/deployment/snapshot/
account identities, healthcheck paths, free-text failure data, nested gateway/
artifact maps, CPU/cost fields and future native DTO fields. No new environment,
log or write authority is granted. Extend performance and wake-history guidance;
prompt retrieval fetches no timeline evidence.

## Consequences

Agents can explicitly inspect retained startup stages for one wake with native
timing and presence semantics. There are 44 read/planning tools, 52 with writes
enabled and six prompts. Protocol and pinned real Codex fixtures cover consent,
identity replacement, errors, missing/zero/false presence, tied times, unknown
fields, pagination limits and empty filtered reads with zero model requests or
native writes. Portable fixtures do not qualify native/provider deployments.
