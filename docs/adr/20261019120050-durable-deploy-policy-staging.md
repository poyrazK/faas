# ADR-20261019120050: Durable pre-submission profiling and tracing recovery

Date: 2026-10-12
Status: Accepted

## Context

ADR-20261019120048 and ADR-20261019120049 recover accepted submissions. Profiling and tracing are
staged before submission because the build pipeline bakes their defaults into
the guest manifest. Their in-memory compensation does not survive process death
or a lost staging reply. A read-then-PATCH rollback also races with other edits.

## Decision

The single-app CLI saves its session and a typed profiling/tracing journal before
staging either policy. The journal stores the owned app ID, latest deployment ID,
policy revision, and selected before/desired values. It never stores manifests,
source, credentials, secrets, environment maps, or cron payloads.

A dedicated authenticated deployment-staging endpoint snapshots the policy
revision and atomically stages or cleans up selected policies under the app-row
lock. It has the same write scope and MFA gates as app settings PATCH. All
operations bind the exact app ID and unchanged latest deployment (including the
absence of a deployment). These operations do not create deployments or builds.

Each app retains only one policy ownership token, its canonical request digest,
and a monotonic policy revision. Normal profiling/tracing changes increment the
revision and clear ownership; explicit same-value policy PATCHes also relinquish
ownership. The database trigger covers other manifest writers, and the memory
store mirrors it. Unrelated manifest edits preserve the token and revision.

Stage checks the captured revision and selected before values, writes both
selected policies and ownership atomically, and increments the revision once.
A matching owned duplicate is read-only. A declared policy already at its desired
value still claims ownership, fencing an older attempt's cleanup. Cleanup
restores before values only while the operation still owns the unchanged policy group. If stage never ran,
cleanup increments the revision without changing policies: a delayed stage is
fenced out. Already restored before values can be recognized without writing
when the captured revision has been superseded. Conflicting edits or newer
deployments leave recovery pending. There is no unchecked fallback.

The CLI persists `submission_started` immediately before the SDK sends a
deployment-creating HTTP request, including resumable upload commit and dev
source. Upload start/chunks are not submission. A previously uncertain upload
commit advances the journal conservatively before receipt discovery. Failure to
persist the boundary prevents the request. Accepted or uncertain submission
preserves policies. Definitively rejected or never-submitted attempts may clean
up. Resume finishes this cleanup from another process without reading source or
submitting a build, and returns a dedicated cleanup receipt.

Old sessions without a journal retain their existing recovery behavior. An old
server cannot stage journaled policies; the CLI fails before submission. Cron,
bindings, secrets, project apply and server-side source-ref staging are outside
this first slice. The obsolete in-memory profiling/tracing compensation helpers
and their superseded tests are removed.

## Consequences and validation

The bounded ownership metadata lives with the app; no operation table or reaper
is needed. Protocol types and guards live in `pkg/api` and `pkg/state`; the
authenticated handlers and callbacks to existing server validation stay in
`cmd/apid`. The size baseline records the intended recovery adapter growth from
ADR-20261019120047, ADR-20261019120049 and this decision. Losing a cleanup reply is safe to retry.
A concurrent unrelated edit is preserved; a policy edit, including an edit away and back, blocks an obsolete
restore. Policy revisions fence delayed requests even after cleanup completes.

Acceptance covers process death after staging and before submission, lost stage
and cleanup replies, cleanup arriving before a delayed stage, concurrent policy
edits and ABA changes, unrelated edits, app replacement/new deployments, unknown
and accepted submissions, journal-write failure before network send, private file
permissions and real PostgreSQL locking. Each cleanup creates zero builds.
