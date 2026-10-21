# ADR-1159: Scope-bound lineage retry files

Status: Accepted
Date: 2026-10-13
Related: ADR-1153, ADR-1154, ADR-1158

## Decision

Default both CLI lineage retry exporters to a local version-1 bundle identified
by `format: gregale.event-lineage-retry`. Include stable app and subscription UUIDs
and the existing API request in `request`. Exports from a finished bulk job also
include `source_job` with its reviewed UUID and original creation timestamp.
All preview cases must share a valid app identity. UUIDs and timestamp zones are
normalized using submission semantics; the file retains one reusable request UUID.
Keep the existing 64 KiB file cap, maximum 200 cases, owner-only permissions,
exclusive destination creation and incomplete-write cleanup.

Create and preflight detect bundles, validate format/version, required scope IDs
and request guards, and reject unknown fields or duplicate names, including
alternate casing, throughout the bundle.
Reserved bundle markers trigger bundle parsing even when malformed, preventing
fallback to raw request semantics. Reject a different subscription locally before
authentication. Read app metadata for the supplied slug and compare its stable
UUID before any submission or case checks. Preflight also pins subsequent
observations to this app identity. Extract only `request` for the existing POST;
local scope and provenance do not alter server DTOs, storage, auth or idempotency.
Neither create nor preflight rewrites the reviewed file.

Continue accepting legacy raw API bodies. Both exporters expose explicit `--raw`
for older CLI versions and direct API/SDK consumers. Legacy raw files have no
scope or provenance guard. App metadata validation requires read scope in addition
to create's existing deploy/write permission. Scoped preflight makes one extra
bounded metadata read, within its existing total timeout. Source provenance is
informational and requires no live source read during create/preflight, so source
retention expiry does not invalidate a saved destination request.

## Consequences

An application event can yield cases in multiple consumers with the same outbox
ID and acceptance timestamp. Legacy cases can also share numeric revisions and
lack diagnostic incarnation IDs. A raw body alone therefore cannot prove its
reviewed target. Scope-bound files help prevent accidentally applying that body
to another consumer or an app slug now owned by a different app. Stable IDs allow
app renames when the operator supplies the current slug.

This is a local CLI interchange format, not a new HTTP envelope or signed access
grant. Older clients must use raw exports; direct integrations must validate scope
and unwrap the nested request. Files are editable, and remote reads can race with
submission. Server ownership and guard validation remain authoritative. No new
API route, SDK method, database column, migration or compliance mapping is needed.
