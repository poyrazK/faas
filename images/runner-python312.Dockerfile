# runner-python312 — base rootfs (drive0) for Python 3.12 apps and functions
# (spec §4.6, §4.9). Same two-drive rationale as runner-node22.
# Content-addressed, staged to /srv/fc/base/runner-python312.ext4.
FROM python:3.12-slim-bookworm@sha256:9c47360a2a0355e2da18516d0b1c2126ec22c195d2185e97347c9d98398c5bef
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get upgrade -y --no-install-recommends && \
    rm -rf /var/lib/apt/lists/* && \
    (id app 2>/dev/null || useradd -u 1000 -m app)
WORKDIR /app
