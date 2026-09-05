# ADR-153 · Disposable developer source deltas

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** transfer changed source entries for `gregale dev`, reconstruct a complete archive from a disposable node-local cache, and transparently resend the full snapshot whenever that cache cannot be used.

## Context

`gregale dev` deliberately deploys the dirty working tree through the same
build and Firecracker path as production. ADR-152 removed repeated dependency
installation for Railpack developer builds, but every edit still uploaded the
complete source archive. Large workspaces therefore paid full network cost for
a one-file change.

Gregale's control plane is moving toward multiple hosts. A source-delta design
must not make a particular apid host or its disk durable state, and it must not
let partial source bypass the existing source validation and build pipeline.

## Decision

The CLI continues to create the existing sanitized complete tarball. This
preserves `.gregaleignore`, secret redaction, workspace-root selection and file
modes. It derives a canonical manifest from archive path, entry type, mode,
size and content SHA-256; timestamps and ownership do not affect the revision.
After a successful first sync, later syncs upload a tar.gz containing only new
or changed entries plus a JSON deletion list. A delta is used only when it is
smaller than the complete archive.

Developer source uses a distinct
`POST /v1/apps/{slug}/deployments/dev-source` route. An empty base revision
means the upload is complete. A non-empty base selects an account- and
app-scoped cached archive. apid streams base + changed entries - deletions into
a new complete archive and verifies the advertised target revision. Only then
does it run the ordinary source-root, stateful-shape, secret-scan, Dockerfile,
function and enqueue gates.

The reconstruction cache is node-local, stores one current revision per
developer app, expires after 24 hours, and evicts oldest entries above a 4 GiB
aggregate node budget. It is an optimization, never source of truth. Missing,
stale or corrupt bases return 409
`dev_source_base_missing`; the CLI retries the same target once as a complete
snapshot. A server without the distinct endpoint returns 404 and the CLI uses
the existing complete-upload endpoint for the rest of that watch session.
Production and pull-request-preview deploys never use this cache or protocol.

## Consequences

Normal source-only edits transfer in proportion to changed content while the
builder still receives the same complete archive shape. Restarts, cache
eviction, request routing to another host, and concurrent developer sessions
can cause a full resend but cannot prevent a valid deployment. The CLI retains
manifest state only for the current watcher process; a new invocation safely
starts with a complete snapshot.

The optimization currently saves network transfer, not local packing time.
Avoiding the complete local pack would require a second trust-equivalent
filesystem manifest/redaction pass and is deferred until transfer behavior is
measured in production.

## Rejected alternatives

- Sending delta metadata to the ordinary deployment endpoint: an older server
  would ignore the metadata and could build an incomplete tree.
- Persisting source bases in Postgres or requiring shared object storage:
  turns an optional optimization into control-plane truth and adds lifecycle
  coupling to developer sessions.
- Keeping builder microVMs alive between edits: weakens the ephemeral builder
  isolation boundary and duplicates ADR-152's content-cache solution.
- Applying changes directly to a builder spool directory: exposes partially
  mutated source to concurrent builds and bypasses full-archive validation.

## Validation

Pure archive tests cover add/change/delete reconstruction, revision mismatch,
path traversal and link rejection. SDK tests pin the distinct multipart wire
shape. apid tests cover full snapshot seeding, delta reconstruction and the
typed cache-miss response. CLI tests pin the automatic full retry.
