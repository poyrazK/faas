# ADR-475: Journal jail directories and named network namespace bindings

Date: 2026-10-02

Status: Accepted; native lifecycle acceptance pending, customer cutover disabled

## Context

ADR-474 records staged files and image binds, but teardown still removes a jail
tree or named network namespace without checking its observed identity. A
replacement at the same path can be mistaken for a resource belonging to the
running owner. Restart cleanup also needs more complete provenance.

## Decision

Write resource journal version 3. Continue reading versions 1 and 2 without
inventing missing placement records. Older binaries reject version 3. Preserve
the existing exclusive writer, bounded records, atomic rename and fsync rules.

Record separate jail assets for the instance directory and its inner `root`.
Resolve the shared parent path, commit each intent before exclusive mkdir, keep
the created device/inode in the live owner's memory, fsync its parent, and
checkpoint before staging contents. Linux records also include kernel boot ID
and vmmd mount namespace inode. Portable directory tests can omit that context;
those records provide less provenance. Failed commits retain cleanup state.

Record a named network namespace intent before `ip netns add`. Execute namespace
creation separately from the remaining setup batch. Require a real nsfs mount
whose NS_GET_NSTYPE is CLONE_NEWNET, capture its device/inode and actual bind
mount ID in vmmd's boot/mount namespace, and checkpoint before topology or policy
setup proceeds. A plain marker, symlink, stacked mount or unreadable observation
does not establish identity. Namespace intents must match the lease's name and
are forbidden for networkless leases.

Prepared spares retain namespace observations in the running pool's memory.
Check them before claim, preserve the nsfs inode and boot/mount namespace across
the alias transfer, and capture the new alias mount ID. Once the Wake lease
intent is committed, persist that placement before policy retarget or guest
launch. Spares and the alias-transfer window before lease intent are not made
durable by this decision; startup inventory still quarantines renamed survivors.

## Live-owner cleanup

Only observations held by the original VMM/Manager authorize these guards.
Never reconstruct its ownership maps from journal records. Refuse an existing
jail without a live owner and refuse changed instance/root directory identities
before unmounting image binds or recursively removing the tree. After tracked
bind cleanup, reject any remaining host-visible mount inside the jail. Remove
the tree, fsync its parent, and retire both jail assets. Failed retirement retains
the live map so retries complete directory fsync without losing identity.

Check a namespace binding before named teardown or prepared-network reuse.
Journal-enabled setup may rebuild an observed owned binding; it no longer
blindly deletes an unobserved stale namespace. Unknown or replaced bindings keep
the lease. After confirming removal, fsync the namespace marker's parent and
retire its asset before releasing ownership. Failed checkpoint/retirement fsync
keeps the original observation for retry.
Jail and namespace absence is accepted only in the same recorded creator
context, including cleanup retries; a different mount namespace cannot prove
that the original resource disappeared.

## Boundaries and remaining work

These are observations at identity checks, not a complete incarnation/ownership
epoch or an atomic compare-and-delete operation. Kernel mount IDs and inode
numbers can be reused. The shared jail parent and child namespaces are not
anchored throughout every operation; concurrent privileged mutation remains
outside this guard. An unmounted marker after a partial `ip netns del` failure
remains conservatively uncertain because its hidden placeholder was not captured.

Veth/ifindex provenance, child TUN and mount setup, loop/parent mounts, jail-local
links/copies, immutable snapshot publication, crash-safe prepared alias handoff,
and filesystem power-loss qualification remain pending. Existing veth teardown
does not gain an identity fence here. Reopened records still quarantine capacity;
they grant no process adoption, serving state, recovered report application or
restart teardown. Verified restart cleanup, complete cleanup receipts and durable
all-node drain proof remain required. Customer cutover stays disabled.

## Validation

Portable race regressions cover jail checkpoint/reopen, changed instance/root
directories, failed intent/checkpoint/retirement fsync, namespace setup ordering,
changed bindings, failed checkpoint/retirement retry and prepared alias transfer.
Metal regressions cover nsfs type validation, namespace replacement, actual alias
mount identity changes, foreign nested jail mounts and reopened guest placement
provenance. Reopen fixtures retain the original live owner for cleanup.

Source-pinned internal-node diagnostics and their limits are recorded in the
[placement evidence](../ops/evidence/20261002-managed-postgres-resource-placement/README.md).
Nested virtualization is diagnostic only; native x86_64 KVM acceptance remains
pending.

Final nested diagnostics passed 41 selected top-level tests (98 including
subtests), three leak checks, Linux race regressions and changed-code lint.
Full portable race suites passed for fcvm and vmmd. Native acceptance remains
pending; the final source also rejects absence observed in another creator context.
