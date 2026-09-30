# ADR-378 · Atomic workload cgroup launch

- **Status:** accepted
- **Date:** 2026-09-30

## Context

Guest workloads were started before their PID was written into `cgroup.procs`.
Main and companion startup probes could run before this write. A process could
allocate memory or fork children during that interval, and migrating the parent
would not migrate existing children. Placement errors were only logged, allowing
a configured workload to continue outside its memory/CPU/I/O leaf.

## Decision

Open the prepared cgroup v2 leaf before launch and use Go's `SysProcAttr`
`UseCgroupFD` / `CgroupFD` to request Linux
[`clone3(CLONE_INTO_CGROUP)`](https://man7.org/linux/man-pages/man2/clone.2.html). The
configured workload starts in its leaf before executing customer code. Startup
fails on descriptor acquisition or kernel placement errors; do not retry outside
the leaf or fall back to post-start PID migration. Workloads without an inner
resource policy retain the existing host-enforced instance scope.

Preserve chroot, process-group, and OCI credentials. Hold the descriptor until
workload and probe completion. Exec startup/readiness/liveness probes that copy
the workload's launch attributes enter the same leaf. Descriptors are opened
close-on-exec and are not inherited by customer code. Reject separators, NUL,
and traversal in both workload type and name when deriving leaf paths. Prepare
control files through a rooted filesystem handle; reject paths outside the
workload cgroup root and symlinks that escape it.

The platform's pinned Linux 6.1 guest kernel supports this clone3 operation.
A custom kernel or syscall policy denying it fails startup for configured
workload leaves rather than weakening isolation. The host cgroup fence,
scheduler accounting, and billing contracts are unchanged. No new customer
resource knobs or quota values are introduced.

## Validation and recovery

Linux unit tests cover launch-attribute preservation, descriptor close-on-exec,
missing/symlink leaves, and failure before command execution when the target is
not a cgroup. A Linux integration test under an explicitly delegated cgroup v2
parent verifies initial process membership, immediate descendant inheritance,
and exec-probe membership under real memory/CPU controls.

Run `make test-container-guest-contract` on Linux as root with
`FAAS_TEST_CGROUP_PARENT` naming a parent whose memory and CPU controllers are
delegated. This is syscall/credential evidence; native microVM, snapshot, OOM
isolation, and leakcheck acceptance remain necessary for fleet qualification.
Retain those results with the release commit. Rollback redeploys the previous
guest artifact and uses the existing snapshot-version/cold-boot policy.

Live main-workload CPU limit updates also open the cgroup root and write through
that rooted handle. A workload-leaf symlink cannot redirect a live update to an
outside filesystem control. Linux regression coverage pins this confinement;
the guest contract runner derives the test from the cgroup test source.
