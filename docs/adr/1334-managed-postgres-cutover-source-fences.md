# ADR-1334: durable cutover source SQL connection fences

Status: accepted for internal controller plumbing; customer activation disabled.

## Context

ADR-1333 reports runtime catalog retirement. Existing and external SQL sessions
can still write after native workloads retire. The provider checkpoint APIs
already authenticate private maintenance ownership, close connection admission,
observe sessions and prepared transactions, and abandon an operation with a
permanent remote tombstone. Those operations need durable cutover ownership.

## Decision

Add an explicit internal `RequestSourceFence` operation and recover it through
the existing cutover worker. Prepare, Verify, Preflight and GET initiate no SQL
fences. No customer fence or activation endpoint is added. New requests require
the provisioning/account gate, fresh staged verification, matching source and
target pins, exclusive source bindings for this app/scope, an existing app-wide
admission fence and a complete retired native catalog. Legacy sources without an
independently recorded data-resource identity are unsupported.

Reserve a private operation UUID and the exact source/backend/generation/spec,
app owner epoch, admission fence and runtime evidence before provider I/O.
Reuse the shared per-dataset maintenance owner, extending its original-owner
reference to permit either a clone operation or a cutover. Each maintenance
dispatch phase is durable before I/O; retries recover its original UUID/OIDs.
Persist the complete authenticated database selection before close dispatch.
Existing sessions are observed, never forcibly terminated by this operation.
Canonical database names and closure identities stay in the private operational
journal as catalog metadata. They contain no connection credentials; only safe
aggregate findings leave the journal. The digest is private, and cancellation
does not depend on fleet envelope keys. The schema registry excludes this
journal and preflight reports from cloned configuration or application data.

Cutover and clone holds exclude one another under a common dataset advisory
lock. Hold insertion requires READ COMMITTED so a waiter observes committed
ownership. Each forward dispatch rechecks account/app/cutover/source authority;
it cannot silently adopt another owner epoch, selection, provider resource or
maintenance database. A worker lease controls progress but never releases the
source hold. Repeated close uses the same private token and selected names.
Independent observations confirm close and terminal abandonment outcomes.
Expired staged verification leaves pending work waiting with durable backoff;
cancellation bypasses that wait and performs cleanup.

Cancellation or authority loss abandons the fence. A never-dispatched close can
be released locally; once dispatch is possible, only an authenticated remote
`released`/`abandoned` tombstone permits local release. A delayed close cannot
survive that tombstone. Database guards prevent cancellation completion, source
unpinning and recovery-record deletion while the SQL hold remains active.
Cleanup and observation continue when new provisioning is disabled. Maintenance
owners persist for reuse; they are not deleted by cutover cancellation.

Preflight stores a historical, safe SQL summary and private evidence digest.
GET detects changed journal evidence; expiry also limits SQL observations.
Names, OIDs, maintenance and operation tokens, provider identities and native
proofs stay private. Specific blockers identify missing maintenance, unconfirmed
admission closure, active sessions, prepared transactions, incomplete database
coverage, observation failures and stale evidence.

## Boundaries and recovery

`selected_databases_drained` covers only the frozen selected database set at its
observation time. It does not prove background-writer coverage or future catalog
stability. `source_sessions_unfenced`, runtime-safety and activation blockers
remain until those independent authorities are qualified. Source writes after
the restore point still need an explicit loss/rollback policy. Routing, snapshot
invalidation, atomic binding publication and controlled resume remain separate.

Downgrade refuses active SQL holds and cutover-owned maintenance that may have
been dispatched. Native VM lifecycle behavior is unchanged. Fixtures establish
recovery contracts, not live provider qualification or permission to activate.
