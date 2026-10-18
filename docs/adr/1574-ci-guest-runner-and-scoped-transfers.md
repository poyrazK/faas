# ADR-1574: CI guest runner and task-scoped transfers

- Status: implemented for internal qualification; disabled and unwired
- Date: 2026-10-15
- Extends: ADR-1573

## Decision

Add a Linux guest workload binary, `gregale-ci-runner`, and an HTTP transfer
handler in `pkg/apid/ciruntime`. The binary executes steps only after verifying
the native Jobs marker and exact command. The handler serves a capability for
one native task, without account credentials or GitHub tokens. PostgreSQL owns
grant state, immutable object receipts and the final result seal.

The scheduler, guest PID1, vmmd, and deployed route table remain unchanged. No
daemon calls grant issuance, mounts runtime routes or starts CI executions.
All CI flags stay off; the image recipe is outside the publication matrix.
This environment has no `/dev/kvm`. Portable tests use simulated steps and VM
receipts; they do not qualify native command execution or VM cleanup.

## Native authority and revocation

`IssueJobsRuntime` is an in-process host seam for future scheduler wiring. It
requires the exact current task-zero/attempt-one lease token, bound instance,
Jobs kind, job/template ID, RAM snapshot, active account, paid Jobs plan,
unchanged materialized image/template and current repository binding. Internal,
GitHub and dispatch gates and the account allowlist must permit issuance.

The task capability is HMAC-derived from the host lease and native run/instance
identity. Only its SHA-256 hash is retained. The host lease never enters the
transfer protocol; capability and host-proof types redact formatting and JSON.
There is no HTTP grant-issuance route. A different instance, replaced or expired
lease, missing native row, or non-Jobs instance cannot exercise authority.

Each request checks durable identity and current policy. Source reads stop on
cancellation, seal, deadline, policy revocation or admission disablement. Control
reports cancellation so the runner can stop its step. Source/object keys alone
grant no access. A stopped/failed VM, terminal non-cancelled task or grant expiry
revokes guest access to every operation.

To retain cancellation artifacts, this ADR makes the earlier blanket revocation
requirement precise: **source and execution permission stops immediately; an
existing grant has at most 20 seconds of output-only finalization**. Explicit
execution cancellation stamps that deadline in the same transaction. Jobs
cancellation timestamps, the absolute CI deadline and observed policy loss also
bound it. Finalization cannot extend the execution deadline, create a new task,
read source or write outside this attempt. Admission flags need not be re-enabled
to finish already-issued output authority. The existing guest supervisor has a
30-second termination grace; the runner's finalization budget fits within it.

## Transfer and publication

The reusable handler defines four routes under
`/v1/internal/ci/runtime/{run}/`: GET `control`, GET `source`, PUT `objects`,
and PUT `result`. Instance and capability are required singleton headers. Mixed
account Authorization, cookies, token query strings and ambiguous headers are
refused. These routes are not mounted by apidserver in this release.

An object upload supplies a relative object name, SHA-256 and exact Content-Length.
The handler privately spools and hashes bounded bytes, reserves an immutable
descriptor, stores the object and rechecks native authority before committing its
receipt. Reservations count toward the 16-artifact/64 MiB total, with 32 MiB per
artifact and a separate 64 KiB log prefix. Retries reuse the same descriptor and
retained object. Unknown names, traversal and undeclared artifacts are refused.

A PostgreSQL lease permits one source/upload/final-seal transfer per attempt
across API processes. HTTP I/O gets 15 seconds and explicit read/write deadlines;
the lease expires after 30 seconds following process death. A token-fenced
release cannot clear a newer transfer. Control does not consume this lease.

The result is sealed in PostgreSQL only after strict JSON, identity, step/exit
validation and matching retained object receipts. The seal is immutable and
idempotent. `result.json` cannot be uploaded as an ordinary object. A dropped
response after sealing can be retried with the identical result.

`SealedJobsOutput` collects the database seal and independently checks object
sizes and checksums. It never trusts an uploaded object named `result.json`.
The backend refuses unsealed output once a runtime grant exists. Native Jobs
status wins races with a guest seal: a native cancellation or later failure
cannot become CI success. VM stop confirmation is still required before the
engine exposes terminal output.

## Guest execution and limits

The runner accepts a bounded v1 manifest and requires `FAAS_JOB=1`, runtime kind
`job`, and `/etc/faas/job.json` matching the account, run and exact runner command.
Only this entrypoint constructs an OS shell executor. Library tests must inject
a fake executor; no repository command runs on the host or in a host container.

The runner verifies compressed bytes and SHA-256 before extraction. Source is
at most 32 MiB compressed, 128 MiB expanded/logical file data, 8,192 entries and
512 bytes per path. It strips one consistent GitHub archive root. Absolute and
traversing paths, `.git`, backslashes, duplicate files, symlinks, hard links,
special files and additional gzip members are refused. Root-descriptor file
operations confine extraction; archive ownership and privilege bits are ignored.

Steps execute sequentially through `/bin/sh -e -c` with an explicit environment
containing PATH, HOME, LANG and CI. Runtime/account/SCM credentials are not
forwarded through that environment. Each step has its own process group;
cancellation sends TERM then KILL after 500 ms, and normal completion kills
remaining group members. Pipe draining is bounded. The VM is the final boundary
for descendants that escape a process group; only native VM stop certifies full
cleanup. Control is polled each second and failure to validate control stops work.

Logs retain a 64 KiB raw-byte prefix with truncation. Artifact collection runs
after success/failure/cancellation, opens regular files within the workspace
root with descriptor-relative no-follow traversal and a nonblocking leaf open,
snapshots bytes privately and publishes the sealed result last. A replaced FIFO
cannot block file validation. Traversal
prunes directories outside literal selector prefixes and remains bounded by
entry count and finalization time. Upload/seal retries are finite. Missing or
partial publication stays unsealed and cannot claim success.

## Packaging and enablement

`make ci-runner-build` creates a static Linux/amd64 binary. The digest-pinned
`images/ci-runner.Dockerfile` packages it with a shell and CA trust; extend that
image with required build tools and pin the resulting platform manifest digest.
The recipe is not automatically built, published, materialized or selected.

Before daemon wiring/enablement, complete native acceptance and the remaining
ADR-1573 requirements: scheduler issuance/injection and policy checks before
VM start, HTTPS route/store configuration, platform receipts for failed boot or
unpublishable output, retention/storage quotas and orphan cleanup, authenticated
artifact downloads, and Jobs image lifetime through recovery. The runtime's
instance FK intentionally retains its recovery evidence; retire grants only
after native stop and output reconciliation, before pruning those instance rows.

Native acceptance must execute a two-job pipeline with this exact binary/image,
not a synthetic shell fixture, and prove step failure, timeout, cancelled and
background descendants, symlink/sparse/archive attacks, lease replacement,
transfer and node/controller restart, metering and tenant wake admission. Run
`test-metal` and `leakcheck` on a dedicated native x86_64 KVM host. Current
portable evidence does not satisfy that gate. No runtime/dispatch activation or
promotion of blocked receipts is authorized by this implementation.

## Evidence

`make test-ci-runtime` runs source, guest guard, TLS-client, PostgreSQL authority
and HTTP integration tests with fake executors and simulated scheduler receipts.
Coverage includes success, failed/cancelled artifacts, empty/bounded logs,
checksum and path refusal, redirect refusal, lost upload/seal responses, sealed
replay, unsealed-output rejection, native lease/instance changes, expiry and
token-fenced transfer recovery. Every CI flag remains off in generated units.
