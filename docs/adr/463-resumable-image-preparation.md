# ADR-463: Resumable image preparation

Status: accepted

Date: 2026-10-03

## Context

A successful builder handoff was recoverable only while its deployment was
pending or building. imaged changes the deployment to imaging before assembling
the app layer. A process crash after that transition leaves an event replay
that silently skips the work, and the build recovery query excludes the row.
The stale-deployment reconciler eventually cancels it after two hours. A crash
between the snapshotting transition and snapshot_prime publication has the
same problem. Direct OCI deployment notifications also used a pending-only
guard.

## Decision

Persist a private deployment_image_preparations row before image preparation
changes status. Keep the original input path, key, size and normalized node
name, independently of the final deployment rootfs. Internal paths and worker
tokens do not enter the public API stage projection.

Use four checkpoints: preparing, layer_published, scan_complete and handed_off.
While preparing, defer the deployment rootfs stamp made by the existing layer
builders. Only after complete assembly, replication, sidecars and the existing
secret gate succeed, publish the final rootfs and layer_published checkpoint in
one PostgreSQL statement and transaction. Until then, the deployment continues
to reference the original export or cache lease, so existing cleanup protects
it. A crash before this commit can repeat conversion from the retained input;
it does not repeat the customer source build.

Hold the existing per-deployment session advisory lock across preparation.
Every begin rotates the claim token without changing the original input or
completed phase. Checkpoint updates, status transitions and rootfs publication
check that token and the permitted nonterminal status. All mutations lock the
deployment first, matching cancellation, then operate on a fresh database
snapshot. Stale claims cannot publish or reopen cancelled, failed, superseded
or live rows. Memory-store tests mirror the contract.

Recovery selects incomplete private checkpoints, including imaging and
snapshotting, before selecting successful builds that have not started. Both
queries are bounded and node-scoped. Direct image deployment events enter the
same preparation path. For a published layer, re-run idempotent replication
and shared-base staging and resume the security scan. After scan_complete,
resume the snapshot handoff. Recheck the current enforce policy, scan freshness
and stored artifact digest before reusing scan evidence; refresh only the scan
when evidence no longer meets the gate. A successful release-task admission also
counts as a durable handoff; its existing terminal notification owns later
snapshot_prime publication.

Shutdown cancellation, checkpoint persistence errors, busy exports and failed
snapshot handoff publication leave work recoverable. Existing invalid-image
and security-gate failures still fail the candidate. The existing stale-row
reconciler bounds recovery for an unavailable owner or repeatedly failing
infrastructure. The predecessor's serving deployment is untouched.

## Consequences

Completed conversion and scan checkpoints survive daemon restarts. The worker
can finish a status-to-event crash window without a customer redeploy. Durable
handoff delivery remains at least once: a crash after event publication and
before handed_off may publish the handoff again, relying on the existing
scheduler/release-task idempotency. This does not promise exactly-once artifact
writes under a network partition. VM lifecycle ownership remains in schedd and
vmmd.

Apply the migration before upgrading imaged. In-flight imaging rows created by
older versions have no trustworthy original-input checkpoint and are not
adopted; the existing stale reconciliation policy still applies. Downgrade or
migration rollback loses checkpoint history, but does not delete deployments
or artifacts. Upgrade every named imaged worker before relying on fleet-wide
serialization; older binaries do not participate in preparation locking.

## Verification

TestImagePreparationCheckpointContract covers source preservation, token
replacement, phase ordering, node routing and terminal fences in memory and
PostgreSQL. TestImagePreparationResumesAfterCrash interrupts assembly,
publication, scanning, the snapshotting transition and handoff completion,
then constructs a fresh handler and recovers without the original event.
TestImagePreparationShutdownLeavesRecoverableInput verifies graceful shutdown.
TestImagePreparationSerializesDuplicateDelivery exercises PostgreSQL locking.
TestImagePreparationRetriesSnapshotHandoff verifies a notifier outage resumes
without repeating assembly or scanning. Prepared-scan tests verify reuse of
fresh evidence and refresh for stale or changed artifacts and a tightened
policy. Publication-failure injection verifies atomic rollback. The migration test checks rollback,
reapplication, constraints and cascading cleanup.
