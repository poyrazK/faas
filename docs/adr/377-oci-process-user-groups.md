# ADR-377 · OCI process user and group identity

- **Status:** accepted
- **Date:** 2026-09-30

## Context

OCI user metadata was normalized by discarding `:group`, and guest launch set
GID equal to UID. An image requiring a distinct group could lose access to its
files or receive unintended access. Main image health checks also ran without
the image's environment and working directory.

## Decision

Preserve an explicit OCI `user:group` in the existing app manifest `user` field.
Keep the legacy normalization of a bare default UID. Resolve numeric IDs and
named identities against the workload image's passwd/group files at launch;
implicit primary groups come from matching passwd entries. A numeric UID absent
from passwd defaults its primary GID to the same value. Unknown implicit named
users retain the existing platform fallback; an explicit unknown user or group
fails launch instead of silently receiving another identity. The built-in app
user remains available as a compatibility identity for legacy images.

Main application, companions, exec probes, and app-task commands use the same
resolver. Companion lookups are confined with Go's rooted filesystem API so
identity-file symlinks cannot read another image. Existing UID/GID and identity
file-size bounds are centralized in the API limits table. Supplementary image memberships are not imported; this change supports primary
identity. The existing UID/GID-zero PID-1-root launch behavior is retained.
Main image health checks use image environment, effective port, and working
directory while preserving the resolved credential.

## Validation and recovery

Portable tests cover numeric and named users/groups, distinct primary GIDs,
missing explicit identities, malformed/oversized files, and root escape.
Native direct-OCI process acceptance checks distinct UID/GID, command,
environment, and working directory across full-rootfs preparation and restore.
Native qualification remains required before claiming fleet acceptance.
Recovery is a redeploy of the previous artifact; snapshots remain caches and
existing cold-boot fallback is unchanged. No instance ownership or scheduling
boundary changes.
