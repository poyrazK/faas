# ADR-795: Publish native capture from one owned descriptor set

Date: 2026-10-11
Status: Publication boundary implemented; capture integration and native acceptance pending.

## Context

Native memory and VM state readers and private-drive freezing each retain the
physical VM lock. Nesting those boundaries deadlocks. Publishing each file in
separate scopes can also mix source epochs from different ownership observations.

## Decision

Retain the physical lock once and acquire the three distinct, exclusive source
locks in canonical inode order. Recheck all source records after acquiring them.
Read memory and VM state through their original anchors, freeze the pinned drive,
and close every descriptor before releasing ownership. Consumers are synchronous.

Publish only into the original qualification capture namespace chosen by vmmd.
Measure the exact descriptor set and retain its metadata through upload. Verify
the digest and length consumed by storage against each measured artifact; reject
changed files or incomplete consumption. Return the existing three-part input
provenance only after all uploads succeed. Account unknown destination allocation
conservatively with logical bytes. Failed or uncertain uploads retain their
original capture namespace and grant no completion receipt; they are not retried
or deleted through a generic storage-key cleanup path.

This is the publication half of the native adapter. The caller must retain the
Manager flight, pause the original pinned task, complete snapshot/create and
measure its machine. Backing publication and durable capture completion remain
owned by Manager. The unfinished capture and serving gates remain closed.

## Validation

Portable descriptor and modeled-journal fixtures cover original bytes, three-part
provenance, lock lifetime, path replacement, failed/short upload, cancellation,
changed source contents, capability refusal and descriptor cleanup. These fixtures
do not establish native snapshot, mount, recovery or regional serving acceptance.

All 177 selected Linux race test events pass, including the new publication and
capture-file ownership tests. Pinned golangci-lint 2.4.0 reports zero issues for
the native VM package. These checks ran on the internal nested GCE node and are
diagnostic evidence; dedicated native KVM qualification remains required.
