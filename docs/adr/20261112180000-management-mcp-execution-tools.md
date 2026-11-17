# ADR-20261112180000: Local management MCP execution tools

Status: accepted

Extends ADR-20261026152009 (focused catalogs) and ADR-20261103193000
(structured recovery). Uses the existing Jobs and managed-output contracts.

## Decision

Add an opt-in `execute` management MCP catalog for existing account-owned jobs:
job inventory, run dispatch/history/status/cancellation, bounded task/log reads,
artifact metadata and native verified download requests. Ordinary customer API
methods remain authoritative for authorization, plan limits, admission,
execution state, retry policy, artifact verification and storage authorization.
No VM lifecycle, scheduler owner, database schema or execution service changes.

This slice is local-account-only. Hosted OAuth and agent-key grants, app-bound
management grants and organization connections advertise no execution tools;
handlers deny these authorities independently. Opaque local tokens still rely
on native resource authorization; discovery makes no API calls. App/environment consent cannot
grant account jobs by selecting a catalog. Hosted execution needs an explicit
job-identity consent and credential contract in a later change.

Read tools are available without writes. Dispatch/cancellation require explicit
connection writes as well as native API scopes. Dispatch invokes an existing
job's current configuration directly; unlike reviewed release/configuration
changes, it creates no expiring release plan and makes no claim to pin a
previously reviewed configuration. Command/environment overrides and job
definition changes are excluded from this slice.

The caller supplies a canonical UUID per logical dispatch. The SDK's existing
request-scoped idempotency context sends `mcp-job-start-<uuid>` to native
admission. Retrying identical inputs retains that UUID across MCP restarts.
Ambiguous dispatch/cancellation errors extend recovery with
`retry_same_request` and `reuse_original_inputs`, without echoing task inputs.
Native request fingerprints and 24-hour receipt retention remain authoritative;
this is dispatch deduplication, not exactly-once job side effects. Native run
IDs and history recover execution state without a connection-local journal.

Job/run projections omit environment/command/input snapshots, credentials,
instance identities and storage keys. Task artifact manifests are validated
with the native bounded parser and projected without URIs. A native download
request must match the selected artifact's name, size and checksum and return
an unexpired HTTPS GET capability without account authorization headers.
Artifact bytes are downloaded by the client and checked by SHA-256, outside
MCP frames. Metadata declaration is not availability verification.

Run receipts report native state, counters and observation time, plus bounded
polling guidance. Mutation acceptance, execution completion and business/output
verification remain distinct. Lists forward one native page and validate its
identity and continuation. Job identity is checked around dependent reads and
mutations; changed identities or inconsistent responses fail closed.

## Validation

Protocol tests cover both supported MCP versions, read-only/catalog boundaries,
hosted/app/organization exclusion, native identity and page checks, bounded logs,
safe projections and exact integer artifact sizes. A native API fixture commits
a dispatch then loses its response; a new MCP connection recovers with the
original request key and creates one run. Changed inputs retain native conflict
semantics. Artifact tests reject inconsistent checksum, foreign task, invalid
manifest, expired/non-GET capability and authorization-header responses.

These portable tests verify the adapter and public MCP protocol. They do not
establish deployed job execution or native Firecracker acceptance, and do not
promote the existing management MCP preview capability.
