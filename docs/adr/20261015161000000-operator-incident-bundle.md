# Portable operator incident bundles

Status: Accepted, 2026-10-15. Extends operator incident evidence, retained
alert episodes, and related context.

## Decision

Add the read-only CLI command
`gregalectl obs bundle --incident type/resource-id --output incident.json`.
Compose the existing sanitized evidence, related-context, and observability
health APIs. Each call retains its admin-scope, MFA, and email-allowlist gates.
No new server route, database writer, migration, frontend, or rollout is needed.
The customer debugger's bundle establishes the metadata-only export pattern;
the operator format has its own versioned schema and coverage semantics.

The schema is `gregale.operator.incident-bundle/v1`. Preserve each API's source
timestamps and a CLI collection timestamp per section, plus collection start
and completion times. These are three sequential reads, not one atomic snapshot.
Status and relationships can change between sections. Relationships describe
current potential exposure, not historical impact or causation.

Resolve the requested incident through evidence first. For legacy platform-alert
series aliases, record both the requested alias and resolved immutable episode
ID; use the episode ID for the context read. Retained evidence intentionally
keeps the series handle in `incident.resource_id`. Reject a mismatched identity
instead of silently combining episodes or different resources.

## Coverage and bounds

Collect exactly one related-resource page; do not follow cursors, trace paths,
runbooks, action links, or other diagnostic links. Preserve the page cursor and
per-section history windows. Resource and history limits default to 25, maximum
100, using the existing shared API limits. A coverage summary exposes evidence
history, related resources, and related timeline truncation separately, and
names unavailable sources with their section prefix.

Keep telemetry signal state, observation timestamps, alert-collection health,
and last-attempt/success timestamps. Mark missing legacy health metadata and
unhealthy signals explicitly. A zero fallback without healthy source evidence
is not an observed zero. Unknown relationship scope keeps null counts. No
global completeness or incident-health boolean is inferred from a successful
export.

All three calls and body reads share one timeout: default 30 seconds, maximum
one minute. Each decoded HTTP body is capped at 1 MiB; the final JSON artifact
including its trailing newline is capped at 4 MiB. Limits live in
`pkg/api/limits.go`. Redirects are refused. HTTP errors, invalid JSON, invalid
identity/snapshot metadata, timeouts, and size-limit failures abort without
publishing a file. A successful API response that explicitly reports missing or
degraded sources remains exportable with those limitations intact.

## Redaction and local publication

Decode only into the sanitized operator API DTOs, discarding unknown JSON
fields. Do not copy raw response bodies, local configuration, credentials,
request bodies/headers, logs, actor identities, commands/environment values,
event payloads, span attributes, or SQL. Error output includes section and HTTP
status but never raw server bodies, URLs, or transport/JSON parsing errors.
Identifiers and allowed operational metadata remain in the artifact and require
controlled sharing; redaction is not anonymization.

Require an explicit output destination. Build the complete artifact in memory,
write and sync an owner-only temporary file in the destination directory, then
publish using a hard link that atomically refuses existing paths, including
symlinks. Remove the temporary name on completion or failure. Filesystems that
do not support hard links fail explicitly. A process crash before cleanup can
leave an owner-only temporary file; no incomplete artifact appears at the
requested destination. `--output -` emits JSON to stdout, where the caller
controls permissions. `--json` selects a structured file receipt; a stdout
export always emits the bundle itself. There are no automatic uploads.

## Validation

CLI HTTP round trips cover all four incident kinds, authentication and limits,
alert-alias pinning, source/capture timestamps, string ID precision, explicit
unknown/degraded/legacy coverage, unknown-field omission, HTTP and JSON failures,
body/artifact limits, redirects, cancellation, and local file permissions,
existing-file/symlink refusal, and temporary-file cleanup. Completion/JSON
contracts and configured lint guard the CLI wiring.
