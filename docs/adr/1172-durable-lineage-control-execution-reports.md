# ADR-1172: Durable lineage control execution reports

Status: Accepted
Date: 2026-10-13
Related: ADR-1170, ADR-1171

## Decision

Add optional --report PATH to batch-control apply, and --resume-report PREVIOUS
requiring a new --report destination. Preserve existing stdout behavior. Reports
are private, append-only JSON-lines files created exclusively with mode 0600;
never overwrite an existing destination. Sync the header and containing directory
before controls, and sync each record. A report creation failure sends no controls.

The versioned header binds the complete normalized plan with SHA-256, including
scope, original guards, actions, UUIDs, order and source metadata. Moving the plan
or changing JSON whitespace does not change this binding. Editing its contents
does. This digest detects mismatches, not authenticity of an editable local file.

Before each POST, sync an unconfirmed intent with the fixed job/command/action.
After verification, sync the acknowledgment and original receipt; failures remain
unconfirmed. Persist not-attempted entries too. A persistence failure stops further
POSTs and exits unsuccessfully while still emitting the in-memory summary. Report
records contain progress and control receipt metadata, never event payloads or
case selections. Limit each report to 1 MiB in pkg/api/limits.go.

Resume strictly reads bounded regular reports, rejects unknown/duplicate keys,
invalid outcomes and entries that differ from the matching plan. Require a complete
header, limit records to twice the plan size, and ignore only an unterminated final
line that a crash may have interrupted. Missing entries mean no durable evidence;
they remain eligible for submission with the original UUID. The intent protocol
ensures previously attempted controls have a complete record before submission.

Inspect every previously recorded entry with existing job and complete receipt
GETs. Stored acknowledgments are advisory evidence, never blindly trusted. A
matching server receipt becomes acknowledged without another POST. Absent receipts
permit submission only when preflight reports ready/no_op. Failed reads, changed
guards, expiration, conflicts, full receipt journals or terminal blockers remain
unconfirmed; independent entries continue. No replacement UUIDs are generated.

Every resume writes a fresh report and leaves the previous report unchanged.
Use the newest report for another resume. Reports are not atomic batches, rollback,
retention reservations, authenticated audit logs or exactly-once delivery promises.
File/directory sync depends on filesystem support; failures prevent further writes
to the server. No API, SDK, schema or compliance registry changes are required.
