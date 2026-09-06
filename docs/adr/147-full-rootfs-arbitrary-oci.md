# ADR-147: Full-rootfs fallback for arbitrary OCI images

Status: Accepted
Date: 2026-09-05

## Context

The two-drive deployment path stores only layers above Gregale's shared
runtime base. An image based on Alpine, Debian, Ubuntu, distroless, or scratch
does not have that layer prefix, so it cannot be assembled as a drive1 delta.
The existing platform-selection work makes the image executable on the
Linux/amd64 fleet, but it does not remove this base-image coupling.

## Decision

When `LayersAboveBase` returns its typed mismatch sentinel, paid plans may
assemble every OCI layer into one self-contained ext4 artifact. The artifact
uses the existing app-layer storage key and wire slot, writes a builder-owned
marker at `/etc/faas/.full-rootfs`, and is signed through the normal storage
pipeline. Guest-init validates the marker and pivots directly into the mounted
layer, preserving the existing two-drive wire format without overlaying the
shared base. Free plans require the explicit full-rootfs override.

The builder derives named-user ownership from the image's `/etc/passwd` and
writes a bounded binary lookup table for guest-init. Sidecars are supported in
the full-rootfs path through a direct-root contract: each sidecar remains a
self-contained optimized artifact, is mounted read-only from drive2 or drive3
under `/run/faas/sidecars/<name>`, and runs chrooted into its own `/upper` tree
with a private mount namespace. Guest-init supplies the sidecar's `/dev`,
`/proc`, `/sys`, and private `/tmp`; deployment overrides stay in the main
instance root and are staged through the existing name-scoped env files. The
sidecar's named user is resolved from its own `/etc/passwd`. The normal
two-drive sidecar path is unchanged. Per-plan unpacked-size and passwd-entry
limits apply, and all marker operations use the layer extractor's symlink
containment checks.

## Consequences

Arbitrary OCI images can deploy on the existing x86 compute fleet without
requiring an image rebuild from a Gregale base. Paid-plan images consume more
per-deployment storage than a shared-base delta, while still using the current
snapshot and replication paths. Full-rootfs sidecars consume one additional
read-only drive per sidecar (with the existing hard cap of two) and do not share
the main image's filesystem or user database.

Validation covers all-layer publication, marker handling, named-user parsing,
guest-init lookup, direct-root sidecar mounting and chroot isolation, plan
defaults, migration persistence, and the complete Go test suite. Metal tests
remain the acceptance gate for real Alpine/distroless boot behavior.
