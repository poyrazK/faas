# runner-python312 — base rootfs (drive0) for Python 3.12 apps and functions
# (spec §4.6, §4.9). Same two-drive rationale as runner-node22.
# Content-addressed, staged to /srv/fc/base/runner-python312.ext4.
FROM python:3.12-slim-bookworm
RUN id app 2>/dev/null || useradd -u 1000 -m app
WORKDIR /app
