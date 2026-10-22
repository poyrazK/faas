# ADR-20261019120004: Server-owned safe project source releases

Date: 2026-10-12
Status: Accepted

## Context

Ordinary project apply admits and activates workloads independently. Partial
failure can expose a mixed graph. ADR-259 already provides immutable release
sets and atomic activation; ordinary source deploy does not orchestrate them.

## Decision

`gregale deploy --project --atomic --project-slug shop --path . --yes` scans the
complete source against the active production graph. The first version supports
existing HTTP source workloads with unchanged membership and configuration.
Revision retention and an active production release set are prerequisites.
Workers in service mode, image declarations, managed resource declarations,
release commands and operation declarations are refused. Secrets and triggers
are preserved; partial selection and persisted exclusions are not allowed.

Safe scan binds the previous graph ID into its plan token. The dedicated
POST /v1/projects/safe-deploy endpoint requires tracked submission identity and
forces safe admission. Older APID cannot accidentally execute ordinary apply at
this path. The CLI also requires a safe scan response before submission.

Safe apply validates the source-only diff and bypasses app reconciliation. Every
workload is rebuilt at explicit zero traffic, using the named production scope
that release sets use, rather than ordinary deploy's default-scope alias. It does
not advance project source checkpoints during enqueue. Release commands and
operation publication are rejected before those effects can execute.

The existing account-owned submission receipt contains the release baseline,
deadline and building/activated/failed/conflicted checkpoint. APID polls these
receipts independently of the CLI and rediscovers them after restart. The
readiness deadline includes one build timeout per member plus the shared grace
period, so a single builder can drain the whole graph. Interrupted admission,
incomplete or failed candidates, cancelled candidates and expired deadlines
cannot activate. A live candidate must also have completed the readiness stage,
which follows the existing HTTP smoke/hosting verification pipeline.

Ready graphs require the existing durable validator artifact preflight. State
locks the submission, project, apps and candidate deployments, compares the
previous active graph, validates complete membership and retention, and publishes
through the existing release-set primitive. The new active graph and terminal
submission checkpoint commit together. Retrying reconciliation after a lost
commit reply returns the same release ID. Operator publication, changed traffic
or ineligible members conflict instead of overwriting intent.

The CLI waits for the release checkpoint, not weighted rollout completion of the
dark candidates. --no-wait returns acceptance; saved-session resume reads the
same operation. Source and arbitrary server messages are not stored locally.
Cancelling the local wait does not cancel server-owned publication.

## Consequences

A failed candidate leaves the previous graph selected for production HTTP
requests. Successful candidates switch as one graph; the previous graph remains
available for its normal retention window and existing guarded rollback tools.
Failed batches retain candidate IDs for inspection and explicit cleanup.

This is source-only HTTP release safety, not atomic database migrations,
configuration deployment, worker activation or an automatic post-cutover rollback
policy. Those require separate execution and rollback contracts. The safe path
rebuilds all members and deliberately avoids enqueue-time source checkpoints.

The new migration adds a partial recovery index to the existing submission table;
no new operation table or VM lifecycle behavior is introduced. Deploy APID and
its worker before publishing updated CLI/SDKs. Dedicated host acceptance remains
required for production rollout; tests here verify orchestration and store
transactions without running microVMs.
