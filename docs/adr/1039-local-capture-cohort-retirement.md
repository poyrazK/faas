# ADR-1039: Retire a local capture as one quarantined cohort

- **Status:** proposed; implementation validated on ext4, native lifecycle acceptance pending
- **Date:** 2026-10-12
- **Decision:** Retire a local capture by moving its four-object `v2` directory into a private, same-filesystem quarantine and deleting only the receipt-matching originals there.
- **Why:** An inode observation followed by pathname `unlink` cannot distinguish the original capture from a replacement at the public key.

## Context

ADR-568 capture objects use four exclusive, receipt-bearing files below one
capture-specific `v2` directory. The local backend can verify each original
file by its open descriptor, inode, parent and content, but it cannot safely
delete an individual pathname after a separate identity check. A replacement
could arrive between `stat` and `unlink`. Consequently local qualification
artifacts remain retained and production qualification stays gated.

## Decision

Retirement acts on the complete four-object cohort. The scheduler first proves
the exact source and restore processes retired and records its authorization in
the native publication journal. The local backend then atomically renames the
capture's entire `v2` directory, with `RENAME_NOREPLACE`, into a private
retirement namespace on the same filesystem. It reopens that quarantined
directory and verifies its device/inode and every remaining file against the
original four receipts before deleting any file. A substituted directory or
object remains quarantined for investigation and is never deleted as the
original. No writer may reuse a capture ID or publish into the private
retirement namespace.

The backend keeps a durable sealed marker in the quarantined directory after
all four objects have been removed. That marker makes a crash between removal
and the publication journal's completion record replayable. Recovery acts only
on the preexisting authorized retirement journal; inventory alone cannot
authorize deletion. A completed journal tombstone permanently prevents restore
of the capture. The quarantine metadata is retained until a separate
owner-fenced compaction protocol exists.

This protocol assumes the host root identity is trusted. It protects against
competing Gregale writers, accidental pathname replacement and crash replay;
it cannot defend against another privileged root process that deliberately
modifies the private quarantine after verification. Root compromise is outside
the local storage backend's trust boundary.

## Consequences and acceptance

The local backend must reject individual receipt deletion. Cohort retirement
must reject mixed keys, mismatched parent identities, symlinks, aliases,
replaced content, unsupported filesystems and incomplete journals. Tests must
cover a replacement before the rename, a crash after the rename, partial
deletion replay, a lost completion acknowledgement, and a concurrent new
capture at the original public path. Formal enablement still requires native
x86_64 KVM capture, restore, leak and restart acceptance, including the full
GitOps graph. This ADR alone changes no production gate.
