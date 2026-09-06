# base-minimal — the shared read-only base rootfs (drive0) under every app that
# doesn't need a language runtime (spec §4.6). Content-addressed, built in CI,
# staged to /srv/fc/base/ and counted ONCE in the 60 GB reserve. imaged converts
# this OCI image into base-minimal.ext4.
#
# Keep it tiny: it is the lower layer of every overlay and any bloat here is paid
# once on disk but affects boot for every app. No package manager; BusyBox
# supplies shell tools and Bash supports Railpack-generated entrypoints.
# The standalone BusyBox version must carry the upstream ash fix: copying a
# Debian backport without dpkg metadata leaves binary scanners seeing 1.35.0.
# The musl variant is statically linked, so it does not replace the glibc ABI
# supplied below for Bash and customer applications.
FROM busybox:1.37.0-musl@sha256:fc6dddc4c44b1bfe37f41cae8e67d1693828e8f42a91862816d7953e2c9d3f23 AS busybox

FROM debian:12-slim@sha256:5ae3c39ebd15e229dcedd5cee596b2497182493d41ff162e824ba13fc1b2b867 AS build
# Issue #197 B3.5 (extension): base-minimal shares the same `debian:12-slim`
# digest as builder-base; the lock entry covers both. The `scratch` FROM
# below is the empty canonical image (no upstream repo) and is exempt
# from the lock.
RUN apt-get update && apt-get install -y --no-install-recommends \
      libc6 ca-certificates bash && \
    rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=build /lib/x86_64-linux-gnu/ /lib/x86_64-linux-gnu/
COPY --from=build /lib64/ /lib64/
COPY --from=busybox /bin/busybox /bin/busybox
# The rootfs skeleton declares /bin/sh as the app user's shell and several
# diagnostic paths rely on it. Scratch does not create symlinks from the
# source image, so install the BusyBox binary at the contract path too.
COPY --from=busybox /bin/busybox /bin/sh
COPY --from=build /bin/bash /bin/bash
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# The app user every guest execs as (uid 1000, spec §4.8).
COPY images/rootfs-skel/ /
# imaged refreshes the arch-matched guest-init as /sbin/init while staging
# this OCI image into bootable drive0. PID 1 must live on drive0 because Linux
# executes it before the per-app drive1 overlay is mounted.
