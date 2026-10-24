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
FROM mirror.gcr.io/library/busybox:1.37.0-musl@sha256:5cec3fc171c87218698e85a52af7087de727372aae264a787b8112901a5b0092 AS busybox

FROM mirror.gcr.io/library/debian:12-slim@sha256:ec31d744f83f4a73447ba045aaab9409997efe5cc706eca4890c1be36cc1606a AS build
# Issue #197 B3.5 (extension): base-minimal shares the same `debian:12-slim`
# digest as builder-base; the lock entry covers both. The `scratch` FROM
# below is the empty canonical image (no upstream repo) and is exempt
# from the lock.
# The final stage copies libraries out of this stage, so upgrade first: a
# security update published after the pinned image must still reach drive0.
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
      apt-get upgrade -y --no-install-recommends && \
    apt-get install -y --no-install-recommends \
      libc6 ca-certificates bash && \
    rm -rf /var/lib/apt/lists/*
# Declare what the final stage copies (ADR 20261023220532). Without dpkg
# metadata a scanner sees no packages here, although every app on this base
# loads this glibc. Keep the status entries of each package that owns a file
# under the copied library trees or /bin/bash, plus ca-certificates for the
# copied bundle; nothing else of dpkg is copied.
RUN set -eu; \
    keep="ca-certificates"; \
    for pkg in $(dpkg-query -W -f '${binary:Package}\n'); do \
      if dpkg -L "${pkg}" | grep -qE '^(/usr)?/lib(64|/x86_64-linux-gnu)/.|^(/usr)?/bin/bash$'; then \
        keep="${keep} ${pkg%%:*}"; \
      fi; \
    done; \
    mkdir -p /tmp/declared/var/lib/dpkg /tmp/declared/etc; \
    awk -v keep="${keep}" 'BEGIN { RS = ""; ORS = "\n\n"; n = split(keep, k, " "); for (i = 1; i <= n; i++) want[k[i]] = 1 } \
      $1 == "Package:" && ($2 in want)' /var/lib/dpkg/status > /tmp/declared/var/lib/dpkg/status; \
    cp /usr/lib/os-release /tmp/declared/etc/os-release; \
    grep -q '^Package: libc6$' /tmp/declared/var/lib/dpkg/status

FROM scratch
COPY --from=build /tmp/declared/ /
COPY --from=build /lib/x86_64-linux-gnu/ /lib/x86_64-linux-gnu/
COPY --from=build /lib64/ /lib64/
COPY --from=busybox /bin/busybox /bin/busybox
# The rootfs skeleton declares /bin/sh as the app user's shell and several
# diagnostic paths rely on it. Scratch does not create symlinks from the
# source image, so install the BusyBox binary at the contract path too.
COPY --from=busybox /bin/busybox /bin/sh
# Railpack-managed tools use `#!/usr/bin/env ...` shebangs. Plain web apps
# intentionally use this minimal base, so the app layer may contain a complete
# Node/Python runtime while still relying on drive0 for env.
COPY --from=busybox /bin/busybox /usr/bin/env
# mise's npm launcher resolves its install directory and plugin name with
# `dirname` and `basename` before it execs Node. Plain web apps keep their
# runtime in drive1 and rely on these shared drive0 tools, so provide the
# BusyBox applets at the standard paths.
COPY --from=busybox /bin/busybox /usr/bin/dirname
COPY --from=busybox /bin/busybox /usr/bin/basename
COPY --from=build /bin/bash /bin/bash
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# The app user every guest execs as (uid 1000, spec §4.8).
COPY images/rootfs-skel/ /
# imaged refreshes the arch-matched guest-init as /sbin/init while staging
# this OCI image into bootable drive0. PID 1 must live on drive0 because Linux
# executes it before the per-app drive1 overlay is mounted.
