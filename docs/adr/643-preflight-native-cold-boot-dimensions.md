# ADR-643: Preflight native cold-boot dimensions

Date: 2026-10-06
Status: Implementation in progress; native acceptance pending.

## Context

ADR-640 verifies the actual running cold-boot machine before reporting success.
A mismatched configuration can nevertheless allocate and execute a guest before
that observation. The native journal already retains the original physical RAM
and, for version 3 regional attempts, original guest RAM and vCPU count.

## Decision

Validate the concrete cold-boot configuration against that retained provenance
before artifact staging and again before creating the chroot. Both ordinary and
job cold boots use the same guard. Direct Boot and no-readiness paths pass through
the guarded core. A restore fallback preserves its original footprint when
replacing its confirmed prior incarnation; it never invents missing dimensions.

Before releasing the configuration FIFO, retain the original per-instance lock,
pin the original task, verify its local producer and daemon ownership, and repeat
the dimension check. Keep this session through the actual running-machine
observation. An invalid configuration writes no bytes to the FIFO. A later
machine mismatch uses the existing teardown path; an uncertain handoff is not
replayed. Cancellation and changed, revoked, recovered or dead ownership refuse.

Historical regional records without original vCPU provenance cannot cold boot.
Nonregional version 1 records retain their existing RAM-only provenance, with
positive requested dimensions and concrete post-boot verification. This does
not turn their requested vCPU into an original regional resource receipt. Legacy
non-native fixtures keep their existing behavior.

## Consequences and validation

This closes the static cold-boot dimension gap. It does not establish snapshot
source dimensions or pre-load compatibility, complete native resource inventory,
run/task authority, capture/pause/migration, inventory reconciliation, or regional
capacity activation. All regional heads remain frozen. Shared global directory
and billing, independent regional provisioning/stores/ingress, and serving from
every selected healthy eligible region remain required for multi-region support.

Portable tests check pre-staging refusal, unchanged prepared provenance, zero
configuration writes on mismatches or ownership loss, lock retention, cancellation
and post-handoff observation. Linux primitive diagnostics on the internal GCE node
do not establish native acceptance. Dedicated native Linux x86_64 KVM test-metal,
leakcheck and recovery acceptance remain required.
