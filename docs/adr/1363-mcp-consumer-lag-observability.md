# ADR-1363: MCP consumer lag observability

Status: Accepted

## Context

Named activity consumers persist progress with revision and generation guards,
but a saved cursor alone does not distinguish retained backlog from expired
history. Operators also need bounded outcomes for consumer conflicts, capacity
and journal failures without exposing identities in Prometheus labels.

## Decision

Extend exact consumer gets with read-time lag metadata. Capture a fresh validated
native checkpoint after reading the saved record; the earlier target-discovery
checkpoint may predate a concurrent acknowledgment. Validate account, immutable
app/project/environment, scope and retention against the original binding.
Continue validating feed access at the saved cursor (head for uninitialized
consumers). If retention overtakes it, use the validated expiry response's newer
bounds. Recheck durable consent, journal health, generation and revision after
native reads. Withhold progress and diagnostics on generation or revision
changes; the caller refreshes after the corresponding conflict.

For saved sequence S, floor F and head H, retained pending events are
H-max(S,F); expired unacknowledged events are max(0,F-S). Reject invalid scoped
bounds and progress ahead of the observed head. Uninitialized consumers omit
both counts rather than choosing a baseline or implying zero backlog. Include
observation time, configured retention window and age of the last
progress-changing acknowledgment. Future-dated acknowledgment timestamps omit
age and explicitly report clock skew. Exact retries and unchanged cursors keep
their original timestamp. Age is neither event age nor a retention forecast.

Use the existing optional private telemetry registry. Count successful consumer
gets by a closed status set and sample retained/expired event counts and valid
acknowledgment ages into aggregate histograms. Never label by account, grant,
consumer name, selectors, endpoint or cursor. Uninitialized gets contribute only
status. Repeated gets are repeated observations; no metric purports to measure
whole-journal backlog or unobserved consumers. Extend request outcomes with a
closed set of consumer revision/generation conflicts, capacity, durable state
requirements/failures and activity expiry/recovery restarts. Arbitrary error
codes still collapse to error. A successful expired get remains a successful
request with an expired consumer status; rejected expired ACKs are failures.

## Consequences

No native API, SQL, migration, journal format change, background job or feed scan
is required. Each get adds one bounded checkpoint read with current identity and
consent validation. ACK/list results do not claim lag; consumers explicitly get
current diagnostics. Inspection can conflict under active acknowledgment, so
callers may refresh. Counts describe a point in time and cannot reserve retained
history, reconstruct pruned events or prove external processing. Existing
retirement generation fences and encrypted journal behavior remain in force.
