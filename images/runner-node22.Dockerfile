# runner-node22 — base rootfs (drive0) for Node 22 apps and functions (spec §4.6,
# §4.9). Built FROM base-minimal so app images built FROM this share every lower
# layer, keeping the two-drive diff (drive1) to just the app's deps + code.
# Content-addressed, staged to /srv/fc/base/runner-node22.ext4.
FROM node:22-bookworm-slim
# Guest runtime user (uid 1000, spec §4.8).
RUN id app 2>/dev/null || useradd -u 1000 -m app
# The function runner shim (guest/runners/node22) is layered in for `type:
# function` deploys at M7; plain Node apps bring their own entrypoint.
WORKDIR /app
