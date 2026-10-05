# ADR-474: Journal staged artifacts and image-bind provenance

Date: 2026-10-02

Status: Accepted; native lifecycle acceptance pending, customer cutover disabled

## Context

ADR-473 persists lease intent and process incarnations, but image staging still
loses its artifact and mount bookkeeping when vmmd exits. Temporary files were
registered after copying, and bind teardown could forget a failed source-mode
restoration. A replacement daemon needs resource provenance before it can safely
reconcile survivors. The running owner also needs reliable cleanup retries.

## Decision

Extend the private vmmd resource journal to version 2 with staged-asset records.
Version 1 remains readable with no asset records; it does not imply a complete
inventory. Older binaries reject version 2 rather than silently ignoring assets.
Retain the existing exclusive writer, validation, atomic rename and fsync rules.
Limit records to 256 KiB, assets to 128 and canonical absolute paths to 4096
bytes. These are parser/storage bounds, not customer quotas.

For storage materialization and boot-time reflink clones, persist a randomized
path intent before exclusive file creation. Capture device/inode and checkpoint
it before copying or reflinking. Register the live owner's path immediately after
creation so checkpoint failure retains cleanup. Payload bytes never enter the
journal. An unsupported reflink can leave a removed-file intent until normal
owner cleanup; absence alone does not establish recovered lifecycle ownership.

ADR-425's immutable cache-link fast path follows the same journal contract:
commit its randomized destination intent before linking, then record the retained
inode before use. Cache eviction does not remove the instance's link. A failed
link can fall back to copying only after confirming destination absence and
durably retiring the intent. Collisions, unknown identities and failed checkpoint
or retirement commits retain cleanup ownership; replaced files cannot be unlinked.

For image binds, persist target/source paths, source device/inode, original
permission mode, read-only policy, kernel boot ID and vmmd mount namespace inode
before chmod, target creation or mounting. Resolve jail parent symlinks before
staging so the recorded target matches Linux mountinfo. Checkpoint the empty target's own
identity before mounting. Capture the actual mount ID immediately after binding,
verify the bound inode matches the source, and checkpoint it after read-only
remount succeeds. The running owner keeps these observations even if the final
journal commit fails. Namespace provenance refers to vmmd's mount namespace;
it does not identify the jailer's child mount or network namespace.

## Live-owner cleanup

Verify staged-file identities before unlinking, then fsync the parent directory.
For binds, require the observed boot/namespace/mount ID to match the live owner's
checkpoint. Unknown, changed or stacked mounts retain cleanup ownership. Confirm
mount absence, verify/remove the uncovered target placeholder, and fsync its
parent before releasing the source permission reference.

Serialize source-mode restoration with new binds. Hardlink aliases of the same
inode share the original-mode policy. Keep the final reference until descriptor-
anchored chmod and file fsync succeed. An unknown journal bind referencing that
inode prevents a mode change; when the current mode already equals this owner's
original mode, no change is required. The new owner never borrows original-mode
policy or cleanup authority from an old journal entry.

Retire each asset only after physical cleanup succeeds. If journal directory
fsync fails, retain retry bookkeeping; a released permission reference is marked
so retries cannot decrement another guest's reference again. The lease/process
record remains until the Manager's complete teardown and retirement gate.

## Recovery boundary and remaining work

These records are provenance, not recovered ownership, serving state or durable
fleet drain receipts. Restart quarantine remains held. A fresh Manager cannot
adopt or destroy a survivor or apply its recovered failure reports. File
identity checks detect path replacement at observation time; device/inode and
mount IDs can be reused and do not provide a complete incarnation or ownership
epoch. Resource removal is not a general atomic compare-and-unlink primitive.

Complete jail-root and network-namespace incarnations, child TUN setup,
transient secret/export loop mounts, parent-overlay mounts, jail-local hardlinks
and copies, and immutable snapshot publication remain outside this inventory.
Verified restart cleanup, complete cleanup receipts, watchdog/routing recovery,
scheduler reconciliation and all-node drain proof remain pending. Customer
cutover stays disabled. Native x86_64 Linux KVM acceptance and filesystem
power-loss qualification are required before release claims.

## Validation

Portable race regressions exercise ordered intent/checkpoint writes, reopen,
failed intent/checkpoint/retirement fsync, replaced files, hardlink aliases,
unknown bind owners, conservative version-1 loading and corrupt records. The
mountinfo parser rejects ambiguous stacked targets.

Metal regressions exercise real read-only binds, shared-source mode restoration,
retirement retry without double reference release, foreign stacked mounts and
replaced uncovered targets. The surviving-guest journal regression additionally
checks reopened image-bind provenance. Only the original fixture owner performs
cleanup; this does not simulate recovered ownership, daemon crash or power loss.

Source provenance and diagnostic results are recorded in the
[internal-node evidence](../ops/evidence/20261002-managed-postgres-resource-assets/README.md).

The final nested diagnostic passed 31 selected top-level tests (78 including
subtests), three leak checks, Linux race regressions and changed-code lint.
Complete portable race suites passed for fcvm and vmmd. Native acceptance and
filesystem power-loss qualification remain pending.
