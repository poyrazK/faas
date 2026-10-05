# ADR-476: Journal and fence host veth identity

Date: 2026-10-03

Status: Accepted; native lifecycle acceptance pending, customer cutover disabled

## Context

ADR-475 checks jail and namespace placement, but ordinary teardown, prepared
network teardown and live private-network replacement still delete host links
by name. Namespace deletion can also destroy a veth through its peer. A foreign
replacement or renamed original can therefore be mistaken for an owned link.

## Decision

Upgrade asset-bearing records to version 4 when they contain veth provenance.
Continue reading versions 1–3 conservatively; older binaries reject version 4.
Each host end records its slot-derived name, a random locally administered unicast
MAC address, link type,
interface index, kernel boot ID and vmmd network namespace inode. This network
context is distinct from the mount namespace used for bind and nsfs placement.
Veth records are forbidden for networkless leases and names outside the slot.
Preserve exclusive journal access, bounded records, atomic rename and fsync.

Commit the creation address and creator context before `ip link add`. Supply the
address in the same link-add request as the veth pair. Query rtnetlink in vmmd's
actual network namespace, retain the observed index in the live owner, and
checkpoint before bridge attachment, peer movement, policy or guest start.
Initial aliases are ignored on the tested kernel. The
[kernel creation path](https://github.com/torvalds/linux/blob/master/net/core/rtnetlink.c)
applies IFLA_ADDRESS before registration, while aliases use the later set-link
path. The initial host-end MAC carries 46 random bits after the local/unicast
bits are set; guest snapshot identity remains governed by ADR-9.
Split batching at each creation; retain batching for the remaining setup.
Apply this to both public egress and private side-links, including live additions.

Only the original running Manager's observations authorize cleanup. A reopened
journal never reconstructs the ownership map. A live creation intent may resolve
an incomplete create only when the actual link has the intended address and type.
Retain observations across checkpoint and retirement failures. A failed live
attachment remains in the cleanup map even if it was never published to Config.

## Cleanup and prepared reuse

Preflight the namespace and every expected or unpublished owned host link before
network teardown. Reject changed index, address, type, name, owner or creator
context, including an absent link observed from another namespace. If the name
is absent, query the recorded index too; a renamed link retains its slot. Delete
checked host ends by index through RTM_DELLINK, confirm absence, and durably
retire their records before namespace deletion. Recheck the namespace before
removing its binding. Unknown replacements retain the lease and remain untouched.

Prepared spares keep live veth observations in memory. Check the namespace and
checkpointed host link before claim, transfer the observation to the adopted
instance, and persist link intent/checkpoint after Wake's lease intent, before
retargeting policy or starting a guest. Failed alias or identity transfer retains
the slot. Prepared spare records and the alias handoff window remain non-durable.

Live private-network reconciliation checks namespace/link identity before policy
or topology changes. Replacement, detachment and failed-attachment rollback use
the same guarded indexed deletion and journal retirement path.

## Boundaries

The 46-bit random address is an accidental-replacement fence, not authentication
against another privileged process. Linux has no address compare-and-delete primitive; interface
indices can be reused between observation and indexed deletion. Creator context
checks are observations rather than an anchor held throughout the operation.
Concurrent privileged mutation and deliberate address copying remain outside this
guard. Veth peers and child TUN resources are not independently journaled.

No schema, RPC, dependency, environment or quota changes are required. Injected
constructors without a journal retain their existing command-runner path; the
Linux daemon's journal-enabled path uses these checks. Verified restart cleanup,
complete resource incarnations, crash-safe prepared handoff, loop/parent mounts,
jail-local links/copies, snapshot publication, filesystem power-loss tests and
durable all-node drain proof remain pending. Customer activation stays disabled.

## Validation

Portable race regressions exercise changed identity fields, renamed and unknown
links, creator changes even on absent resources, journal intent/checkpoint/
retirement failures, unpublished private attachments and prepared replacement.
Linux parser tests reject malformed rtnetlink attributes. Metal diagnostics
exercise real veth creation addresses, indexed deletion, foreign dummy/veth links,
renames, MAC changes and prepared namespace alias transfer with a real veth peer.

Results are recorded in the
[link evidence](../ops/evidence/20261003-managed-postgres-resource-links/README.md).
The internal GCP node uses nested virtualization; supported native x86_64 KVM
acceptance remains pending.

Final nested diagnostics passed 49 selected top-level tests (126 including
subtests), three leak checks, full macOS fcvm/vmmd race suites, bounded Linux
regressions, generated deployment checks and changed-code lint. Native acceptance
remains pending.
