# ADR-150 · Correct timer register restore order in the SSD canary

- **Status:** accepted for the GCP SSD test canary; fleet release pending validation.
- **Date:** 2026-09-05
- **Scope:** temporary exception to the upstream binary pin in ADR-005. The
  Firecracker snapshot format and reported compatibility version remain 1.7.0.

## Context

The idle basic-function measurement returned HTTP 503 while VMMD waited five
seconds for the guest resume ACK. The retained snapshot reproduced that failure
without the scheduler or gateway. A plain TCP probe released 22 stalled original
resume exchanges in a 30-restore diagnostic run, without repeating the hook.

Firecracker 1.7 restores saved model-specific registers in their stored order.
KVM needs the CPU clock (`MSR_IA32_TSC`) before its timer deadline
(`MSR_IA32_TSC_DEADLINE`). An incorrect order can arm the timer with the wrong
expiry and leave guest processes asleep. Upstream
[PR #4666](https://github.com/firecracker-microvm/firecracker/pull/4666) fixes the
ordering when creating snapshots. Our retained legacy snapshot needs the order
corrected during loading as well.

## Decision

Test a minimal patch against the exact Firecracker 1.7.0 source commit and its
pinned Rust 1.76.0 compiler. Before restoring MSRs, copy each saved chunk with
its deadline entries removed, then restore those deadline entries last. Preserve
all register values, original ordering among other registers, KVM batch limits,
and error handling. Do not change zero deadline values or the serialized format.

Record source, patch and binary checksums. Select the patched executable through
an SSD VMMD-only systemd PATH override. Keep the installed upstream binary and
jailer available for rollback. This decision does not change the fleet Ansible
release pin. The patch is a bounded canary fix while a supported upstream runtime
upgrade is evaluated separately.

Guest entropy injection, CRNG reseeding, clock correction and readiness ordering
remain unchanged. This does not add a TCP wakeup probe or a resume-hook retry.

## Validation and rollback

Require register-order and value-preservation regression tests, retained-input
metal restores with unique UUIDs and no cold fallback, and resource leak checks.
Then measure the same basic function through the SSD gateway with zero live
instances before every timed request. Keep failures in the reported results.
Component timing alone does not establish the full-wake p95 <350 ms target.

Rollback removes only `zz-ssd-tsc-order-canary.conf` from the SSD VMMD service,
reloads systemd, and restarts VMMD when the host has no live guests. The original
Firecracker/jailer symlinks and snapshot format are retained.
