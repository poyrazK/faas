# ADR-166 · Container cross-drive whiteouts

- **Status:** accepted
- **Date:** 2026-09-07
- **Decision:** The optimized two-drive app build preserves OCI whiteouts in
  the published `/upper` tree. Regular whiteouts become overlayfs character
  devices (major/minor `0/0`), and opaque-directory whiteouts become the
  `trusted.overlay.opaque=y` xattr (with a user-xattr fallback while staging).
  Complete-rootfs and base-image assembly keep their existing materialized
  deletion behavior.
- **Why:** Gregale mounts the shared base image read-only and layers each app's
  ext4 as an overlayfs upper. Deleting a path that exists only in the shared
  base cannot be represented by removing it from the app staging directory;
  the base path reappears in the guest unless the upper artifact carries the
  kernel's whiteout marker.
- **Consequences:** OCI images that remove base files behave consistently in
  both full-rootfs and space-efficient two-drive deployments. A later layer
  that recreates a deleted path clears the sibling whiteout marker. Linux
  image assembly must retain device nodes and xattrs through `mkfs.ext4 -d`.
  Non-Linux developer builds retain the historical materialized behavior.
- **Rejected alternatives:** Forcing every image through full-rootfs would
  avoid the marker but discard the shared-base storage and boot-time reuse
  benefits. Leaving whiteouts as `.wh.*` regular files would make the marker
  visible in the guest and would not hide the lower drive. Ignoring cross-drive
  deletions would make valid OCI images semantically incorrect.
