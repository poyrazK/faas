# ADR-605: Verified runtime upgrade source handoff and private worker supervision

Status: accepted · 2026-10-06

## Context

ADR-604 can recover a registered operation, but expects a pre-uploaded candidate
and stops an executor invocation after infrastructure errors. Private admission
needs an exact source handoff, and a worker must recover without a human retrying
every failed poll. Customer execution remains closed pending native acceptance.

## Decision

Add private apid `runtimeupgrade.Stager.StageAndRegister` for an already reserved
managed-function candidate. Reservation remains the caller's responsibility:
retain reviewed overrides, scope and handler, clear mutable Git branch intent,
use explicit zero traffic, and bind `SourcePath` to `CandidatePath(operationID)`.
The operation UUID also names the stable build and source object. Validate a
canonical operation UUID, nonnil owned UUIDs and lowercase hashes before I/O.
Require the owned active
function app, a deployable account, supported source kind, matching source digest,
positive exact size within its current plan cap, build root and handler. Reject
an already building/nonpending candidate, existing output, canary/service/held
graph shapes and mutable candidate Git intent. The database registration fence
remains authoritative for runtime compatibility, sidecars, scoped inputs,
serving identity and qualification.

Read `sources/<serving-build-id>.tar.gz` through the existing source backend.
Fallback to the retained serving spool only when the object is missing, never
after permission/network errors or integrity failure. No Git ref is fetched.
Verify exact byte count and SHA-256 while copying at most the declared size;
read one extra byte to detect an oversized stream. Stream with cancellation
checks. Local paths must be absolute within the configured spool; use `os.Root`
to confine resolution, reject symlink components/nonregular files, and open with
no-follow/nonblocking flags before repeating file type/size checks.

Write a private temporary file in the spool, verify it, mark it read-only and
fsync it, then publish by a nonoverwriting link and fsync the directory. Reuse
an existing destination only after verifying and syncing the file and directory,
including an uncertain earlier publication. Verify an existing canonical
candidate object without replacing it; otherwise upload the verified file as
`sources/<operation-id>.tar.gz` and verify its canonical read after publication.
Builderd still independently verifies source integrity at consumption. Storage
is within the trusted control-plane boundary; the shared backend has no CAS
contract and this does not prevent a privileged operator changing an object.

Register only after both configured handoffs verify. ADR-604 atomically captures
the baseline, pin and journal after storage I/O and rechecks the serving and
qualification fences then. This is the private preparation checkpoint; no
customer preview-to-apply approval contract is introduced. A committed identical
operation returns its retained history before storage I/O, including after
rollback. Changed intent conflicts. Keep published handoffs on uncertain
registration/upload outcomes so a lost response cannot erase committed work.
Remove temporary files on ordinary failure. Orphan and blocked handoff cleanup,
reservation recovery and customer status/cancel controls remain future work;
no automatic candidate replacement or new baseline is introduced.

Supervise `Executor.Run` with context cancellation and bounded steps. Failed
claim/advance iterations retry at 5, 10, 20, then at most 30 seconds; a successful
poll resets backoff. Each retry claims anew and lets an abandoned token expire.
Observe each completed iteration for liveness and sanitized failure telemetry.
A hung iteration never beats liveness. These bounds live in `pkg/api/limits.go`.

Add an explicitly selected `apid --runtime-upgrade-worker` process mode, mutually
exclusive with the clone worker. It performs normal apid environment, capability
and control-plane-role checks, opens its own named database pool, migrates, and
runs only this worker. It starts no API/metrics listener, provider reconciler,
source reservation endpoint or VM client. Establish initial systemd readiness
after the first successful claim or idle poll; watchdog liveness observes loop
progress with a lease-plus-backoff budget. Log no raw database error values.
Normal startup never enables the worker; no service unit/deployment change is
provided, and customer previews keep `execution_available=false`. Each operation
still requires exact unrevoked native qualification and fresh candidate cold
boot acceptance before activation through ADR-602/603.

## Consequences

The private updater can verify and resume source preparation and survive
transient storage/database failures without duplicated build admission. This
does not enable customer runtime updates or prove gateway convergence/drain,
continuous health, native VM lifecycle behavior or retirement safety.

## Validation

Memory and real PostgreSQL contracts exercise source staging, lost registration
responses, stable queue recovery and untouched serving traffic. Synthetic
storage fixtures cover corrupt/truncated/oversized archives, a missing object
with local fallback, storage outages, successful but corrupt uploads, uncertain
upload responses, concurrent nonoverwriting publication, path escapes, symlinks,
FIFOs, wrong ownership/handler and qualification revocation during publication.
Virtual-time tests check capped/reset backoff, failed advance recovery,
cancellation during backoff, bounded hung claims and worker logs without raw
database diagnostics. These fixtures supply no
native acceptance evidence; spec §14 / STATUS still require dedicated Linux
amd64 KVM `test-metal` and final `leakcheck` before customer enablement.
