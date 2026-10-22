# ADR 1089: Python realtime incident timeline

Status: Accepted; runtime validation deferred.

## Context

Diagnostics snapshots show current state but not how a reconnect, denial or stalled replay developed. Applications need bounded local history without payload/secret capture.

## Decision

Export generator-preserved immutable RealtimeIncidentEvent and RealtimeIncidentTimeline. Record explicit callback observations for connection, subscription and catch-up states, including ACK-missing transitions. Store only UTC capture time, monotonic elapsed time, local ID, allowlisted state, attempt/generation and channel/inbox identity. Exclude payloads, presence state, reader IDs, arbitrary codes, exception bodies, tokens, credentials, transport URLs and labels.

Suppress identical state/attempt/generation/warning observations. Bound event ring to 256 (1–8192), counting oldest-event eviction. Independently bound recent transition markers to 1115 (1–8192), evicting least-recently-observed identities. Snapshot is immutable and oldest-first; clear resets all local history/counters. Use one endpoint/account/execution context.

Add an optional timeline section to version-1 diagnostics using the existing channel redaction map. Keep newest events under max_entries, expose omitted counts separately from ring eviction and retain total output cap. No automatic callback attachment, upload or persistence.

## Consequences

Timelines reflect observed callbacks, not durable server history. Revisited evicted markers can repeat an observation. Raw snapshots contain channel IDs; diagnostics is the redacted sharing path. No routes, grants, schema or migration changes. Static lint/compilation, SDK AST/export/preservation and explicit-field checks verify implementation; runtime validation remains deferred by user request.
