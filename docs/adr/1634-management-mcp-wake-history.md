# ADR-1634: Retained wake observations through management MCP

## Status

Accepted. Extends the app-wide diagnostic consent of ADR-1618 and performance
workflow of ADR-1620 using the existing typed GetAppWakeTimeline client. No
native endpoint, persistence, migration or VM lifecycle change.

## Decision

Add read-only gregale_get_wake_history. Check diagnostic consent, live native
ownership/policies and immutable app identity before and after retrieval. Native
Hobby+ plan entitlement remains authoritative. Read one native response without
automatic event/request enrichment, retries or broader ranges. Explicit DTOs
exclude app URLs/status, trigger histograms, full events, infrastructure and
deployment payloads and future native fields.

Accept bounded RFC3339 since/until and resolve default now/24h once. Normalize
to UTC without dropping fractional precision. Preserve submitted timestamps as
requested_since/requested_until with window_verification not_echoed_by_native:
the endpoint has no effective-window echo, source status or retention flag.
Native filtering is inclusive when started_at exists, but selects the latest
100 instance candidates first. Older historical ranges may miss data even when
fewer rows return. Missing started_at bypasses native filtering. Row timestamps
are formatted to whole seconds after source-precision filtering, so validate
only bucket compatibility, not exact membership. Mark missing time unverified.
Counts describe selected instance records, not distinct wake attempts, full
event histories or requests. Rename native wake_count_24h to selected instance
count, since its meaning follows the requested range.

Centralize output default/max 20/50, native candidate maximum 100 and timestamp
bound 64 bytes in pkg/api/limits.go. Output limits only select a newest-first
prefix; never forward an unsupported native query limit. Validate all native
rows before projection, including omitted rows. Verify app ID/slug, timestamps,
kind, newest-first order at native precision, counts, metadata bounds and native
capacity arithmetic. Missing or duplicate legacy wake IDs are permitted; unsafe
nonempty IDs fail rather than being rewritten. Redact then bound display labels
and expose modifications/truncation without treating them as selectors.

Only positive native readiness durations are measurements. Native -1 or zero
means not_reported, not failed/unfinished or zero latency. Both queued_count and
concurrency_at_admit come from the admission concurrency ledger. Expose that
basis explicitly; do not interpret queued_count as waiting queue length or
duration. These scalar zero values lose presence in the native DTO: omit the
number with zero_or_not_reported, even if other metadata is known. Capacity has a separate
presence flag: observed false stays false; absent stays unknown even if its raw
boolean is true. Native at-capacity percentage divides known true flags by all
instances with metadata, including rows whose capacity is unknown. Expose that
denominator, present/unknown counts and the partial-denominator status; omit
percentage when no metadata exists. Do not call it an all-wake saturation rate.

Native instance/metadata read failures can be swallowed into empty/partial
responses. Always report source_status not_reported, source completeness and
freshness unknown; native as_of only reports query time. No returned rows does
not establish no wakes, complete capture or healthy operation. Retained instance
state and boot labels are not full event timelines, live outcomes or causal
proof. Do not sum readiness with request latency or infer changes/savings.
Extend performance guidance without fetching evidence during prompt retrieval.

## Consequences

Agents can inspect triggers, restore/cold-boot methods, tiers, positive readiness
durations and admission measurements while retaining native uncertainty. The
catalog has 43 read/planning tools, 51 with writes enabled and six prompts.
Protocol and pinned real Codex fixtures cover diagnostic consent, native errors,
identity replacement, empty history, missing scalar presence, observed false,
full-response validation, bounded privacy and zero model requests/native writes.
Portable fixtures do not qualify native/provider deployments.
