# ADR-1573: Dormant CI adapter to Gregale Jobs

- Status: accepted for internal contract qualification; unwired and disabled
- Date: 2026-10-15
- Extends: ADR-1576, ADR-1571, ADR-1572

## Decision

Implement `ci.JobsBackend` against ordinary Gregale Jobs intent. apid remains
the owner of admission and job-run intent; schedd alone claims tasks, admits
capacity and owns instances, and vmmd alone operates Firecracker. This change
does not modify the scheduler, guest supervisor or VM lifecycle. It provides no
HTTP route, CLI command, webhook transition, background dispatcher, or deployed
runner image. Its integration tests use real PostgreSQL and synthetic compute
receipts. They do not establish that CI commands execute in a VM.

`FAAS_CI_DISPATCH_ENABLED=0` is an additional exact-`1` boot gate. Internal CI,
GitHub admission, and the account UUID allowlist must also permit admission.
Generated apid units explicitly keep dispatch off. Turning flags on alone cannot
start a worker. Cancel/Observe remain usable with admission off, so rollback of
admission cannot block cleanup.

## Fresh admission and execution identity

`AdmitJobs` is an explicit Go call for a future apid wiring layer. It requires a
current repository binding and an account/installation/repository verifier. The
store locks and rechecks the binding generation after the external verification.
It refuses inactive/abuse-held accounts and plans without Jobs. A fresh execution
has mode `jobs` and a frozen compute/source snapshot. Qualification executions
cannot reach this backend, cannot replay a Jobs idempotency key, and remain
separate from these admissions. Blocked `ci_runs`, including webhook receipts,
never enter the execution ledger or become dispatchable.

Admission selects an existing account-owned, active, materialized batch Jobs
template for every CI job. Its image reference and resolved platform manifest
digest must match the pinned CI image. The template must declare command
`/usr/local/bin/gregale-ci-runner v1`, have an empty environment and no failure
rules. This declaration is a protocol precondition, not proof of a qualified
runner binary. A native-qualified image implementing this protocol is required
before wiring a real dispatcher.

The initial adapter uses the existing Jobs profile of 1 vCPU and 512 MiB.
Timeout must fit the current account plan. Each attempt becomes exactly one
task, parallelism one, retry budget zero, an explicit immutable command/image/RAM
snapshot, empty environment and an absolute queue start deadline. All normal
Jobs concurrency, capacity admission, cold boot, image retention, billing and
cleanup remain in force. No privileged container, host shell, Docker daemon,
snapshot reuse, injected SCM credential, deployment secret, cache, or artifact
transfer between CI jobs is added.

## Atomic fencing and recovery

`ci_job_attempts` journals the stable attempt identity, fence, cancellation
tombstone, source receipt, ordinary Jobs run ID and verified output. Run ID is
the attempt UUID. The execution lease/fence, intent journal and Jobs run/task
insertion are checked/committed in one transaction. Account -> execution ->
intent is the lock order. A repeated Start finds the same run, including after
a response is lost or the controller restarts. Ordinary Jobs replay rejects
these owned runs, and the task retry store rejects CI ownership independently
of API retry-budget checks. A new CI admission is required to execute user
commands again.

Source staging takes place outside the transaction. A cancelled execution,
changed binding/template, expired deadline, or newer fence cannot commit a late
source fetch into a new Jobs run. Cancel before Start persists a tombstone.
Cancellation of an existing run uses the ordinary Jobs cancellation projection;
schedd still performs the stop. A terminal task receipt alone is insufficient:
the corresponding instance must exist in `stopped`, or the task must never have
had an instance. A missing instance row, failed destroy, unexpected task retry,
or unreadable output fails closed and leaves the CI execution pending.

Metadata cancellation invalidates in-flight engine commits; an active engine
lease may need to expire before another controller can reconcile cancellation.
This adapter preserves that existing lease policy.

## Source and output protocol

`GitHubArchiveSource` revalidates the account-owned installation and immutable
repository ID, then uses githubd's existing `StreamSourceRef` RPC with the full
40-character SHA. It checks the returned SHA, caps compressed bytes at 32 MiB,
hashes opaque bytes into a private temporary file and publishes an account /
execution / attempt-scoped content-addressed object. The temporary file is
removed on success/error. No archive is extracted and no repository command
runs on the host. GitHub tokens remain in githubd. Provider error strings are
redacted at the adapter boundary.

The encoded command argument is a bounded 16 KiB JSON `JobsManifest`: exact
source identity/receipt, ordered steps, absolute deadline, artifact selectors,
and output prefix. It contains no URL or token. A future runner and transport
must enforce this protocol inside the VM. Merely knowing an object key must
never authorize source downloads or result writes.

`BlobJobsOutput` validates a final `JobsResultManifest` published last under the
attempt prefix, including on failure/cancellation. It strictly checks JSON,
identity, scheduler status/exit code, engine step ordering, declared artifact
paths, counts, sizes, and SHA-256 of every artifact and retained log blob. It
does not use Jobs' success-only output manifest, which discards failed-task
artifact claims. Verified terminal output is frozen in the journal before byte
pages are exposed to the engine; later object replacement cannot alter replay.
The retained log prefix is 64 KiB, with 16 KiB pages and explicit truncation.
Up to 16 artifacts may contain 32 MiB each, at most 64 MiB per job. The guest
result manifest is 32 KiB and the journal output snapshot is 256 KiB.

This version collects ordered steps and logs at terminal receipt. Live progress
and streaming guest logs are not wired. Missing/corrupt final output remains
pending rather than certifying success or silently discarding artifact evidence.
A queued cancellation that never had a VM requires no guest result.

## Enablement prerequisites and limits

Before adding daemon admission/dispatch wiring, implement and qualify:

1. A pinned runner image and attempt-scoped source/download, log/result and
   artifact/upload transport. Check archive traversal, symlinks, expanded-size
   and disk bounds inside the VM. Revoke grants on cancellation and stop.
2. Runtime ownership/admission checks at the scheduler boundary for CI work,
   including plan downgrade, abuse hold, binding revocation and image changes
   while queued. The legacy Jobs Free-plan fallback is not CI authorization.
3. Guest step termination and descendant cleanup, bounded artifact collection
   on success/failure/cancellation, and a recoverable platform receipt when a
   guest boots badly or cannot publish its final result. Test transport outage
   and controller/scheduler/node restart at each durable boundary.
4. Source/output retention, account storage quotas, orphan cleanup for staging
   that loses a fence, artifact download authorization, and image lifetime
   through all terminal output recovery. Retained execution quotas are finite;
   no pruning or public retention promise is included here.
5. A real two-job pipeline on a dedicated native x86_64 KVM test node, alongside
   tenant wakes under capacity pressure, with Jobs metering evidence,
   `make test-metal` and `make leakcheck`. Nested/container unit tests do not
   replace native acceptance.

Keep all flags off and leave the dispatcher unwired until these requirements
are met. There is no automatic promotion of old metadata or qualification work.
Rollback refuses to drop the adapter journal while any Jobs executions remain;
reconcile live work before explicitly removing its recovery records.

## Validation

`TestCIJobs` applies the full production migration set to isolated databases and
uses fake GitHub/object storage and scheduler/guest receipts. It covers a two-job
pipeline, failure/cancellation artifacts, cleanup gating, terminal log replay,
lost Start responses, stale fences, concurrent Start, cancellation during source
staging, source/binding/template drift, quota, ownership and rollback guards.
Blob tests cover exact SHA, archive bounds, traversal, foreign object prefixes,
checksum mismatch, ambiguous JSON and output limits. The state integration test
confirms CI tasks cannot be replayed through the ordinary Jobs API. These are
contract qualification evidence only; no live compute or GitHub publication.
