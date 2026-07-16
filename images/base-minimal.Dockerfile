# base-minimal — the shared read-only base rootfs (drive0) under every app that
# doesn't need a language runtime (spec §4.6). Content-addressed, built in CI,
# staged to /srv/fc/base/ and counted ONCE in the 60 GB reserve. imaged converts
# this OCI image into base-minimal.ext4.
#
# Keep it tiny: it is the lower layer of every overlay and any bloat here is paid
# once on disk but affects boot for every app. No package manager, no shell tools
# beyond busybox.
FROM debian:12-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
      libc6 ca-certificates busybox && \
    rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=build /lib/x86_64-linux-gnu/ /lib/x86_64-linux-gnu/
COPY --from=build /lib64/ /lib64/
COPY --from=build /bin/busybox /bin/busybox
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# The app user every guest execs as (uid 1000, spec §4.8).
COPY rootfs-skel/ /
# guest-init is injected as /sbin/init by imaged at app-layer build time, not here.
