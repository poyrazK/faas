# ADR 1088: Python realtime diagnostics export

Status: Accepted; runtime validation deferred.

## Context

Support/debugging needs a coherent local report of status, subscription failure, replay progress and unread freshness without copying payloads or credentials.

## Decision

Export generator-preserved export_realtime_diagnostics. Return deterministic-key version-1 JSON from supplied health and optional unread snapshots, with UTC capture time. Serialize a strict whitelist of typed states, sequence/count/timing observations and readiness flags. Never serialize entire dataclasses, exception objects, free-form labels/codes, payloads, presence state, credentials, tokens or transport URLs.

Alias channel and reader IDs by default, maintaining within-report correlation without stable cross-report hashes. Explicit include_identifiers opt-in includes only supplied channel/reader IDs. Bound detail arrays to max_entries (256 default, 1–8192), expose omitted counts and cap JSON at 256 KiB. Encode sequences/unread integers as decimal strings; totals remain complete known sums when rows truncate. Preserve null for missing optional observations.

## Consequences

Report snapshots are caller-captured and not atomic server state. Redaction does not anonymize descriptive counts/timestamps. No file access, upload, network, timer, route, grant, schema or migration is introduced. Static lint/compilation, SDK AST/export/preservation and explicit-field inspection verify implementation; runtime validation remains deferred by user request.
