#!/usr/bin/env bash
# Build the agent-base root filesystem the way images/agent-base.Dockerfile
# does, in a chroot, and pack it as one gzipped tar layer for a local
# registry. For native hosts with no BuildKit; run as root.
#
#   build_agent_base_layer.sh WORK CONTEXT PROBE_DIR
#
# CONTEXT holds images/agent-base.Dockerfile, images/agent-base/ and
# pkg/agentpresets/presets.json from the checkout; PROBE_DIR holds
# oci_pull.py (scripts/experiments/agent-runs/memory). The layer is
# WORK/agent-base-layer.tar.gz. The steps follow the Dockerfile one for one;
# its ENV lines are image config, not files, and the CI-only proxy CA secret
# is not used.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

work=$1 context=$2 probe_dir=$3
dockerfile=$context/images/agent-base.Dockerfile
root=$work/rootfs
from=$(sed -n 's/^FROM \([^ ]*\).*/\1/p' "$dockerfile" | head -1)
[[ $from =~ ^([^/]+)/(.+):[^@]+@(sha256:[0-9a-f]{64})$ ]] || { echo "build: cannot parse FROM $from" >&2; exit 2; }
registry=${BASH_REMATCH[1]} repo=${BASH_REMATCH[2]} digest=${BASH_REMATCH[3]}

unmount_root() {
  local m
  for m in "$root/dev" "$root/proc"; do
    if mountpoint -q "$m"; then umount "$m"; fi
  done
}
trap unmount_root EXIT
mkdir -p "$work"
[[ ! -e $root ]] || rm -rf --one-file-system "$root"

echo "build: FROM $registry/$repo@$digest"
python3 -I "$probe_dir/oci_pull.py" "$registry" "$repo" "$digest" "$root"

# COPY --chmod=0644 images/agent-base/package.json images/agent-base/package-lock.json /opt/gregale/agents/
install -d "$root/opt/gregale/agents" "$root/usr/share/faas"
install -m 0644 "$context/images/agent-base/package.json" "$context/images/agent-base/package-lock.json" "$root/opt/gregale/agents/"
# COPY --chmod=0644 pkg/agentpresets/presets.json /usr/share/faas/agent-presets.json
install -m 0644 "$context/pkg/agentpresets/presets.json" "$root/usr/share/faas/agent-presets.json"
# COPY --chmod=0755 images/agent-base/finish-install.sh /tmp/finish-install.sh
install -m 0755 "$context/images/agent-base/finish-install.sh" "$root/tmp/finish-install.sh"

mount -t proc proc "$root/proc"
mount --bind /dev "$root/dev"
cp /etc/resolv.conf "$root/etc/resolv.conf"
# RUN, as in the Dockerfile.
chroot "$root" /bin/sh -c 'apk upgrade --no-cache && \
  apk add --no-cache bash git python3 ripgrep libstdc++ libgcc && \
  chmod 0755 /opt/gregale /opt/gregale/agents && \
  cd /opt/gregale/agents && \
  NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev --no-audit --no-fund && \
  /tmp/finish-install.sh && \
  npm cache clean --force && rm -rf /root/.npm /tmp/* && \
  if id app >/dev/null 2>&1; then :; \
  elif id node >/dev/null 2>&1; then sed -i "s/^node:/app:/" /etc/passwd; \
  else adduser -D -u 1000 app; fi'
# RUN mkdir -p /input /work/output && chown -R 1000:1000 /work
chroot "$root" /bin/sh -c 'mkdir -p /input /work/output && chown -R 1000:1000 /work'
unmount_root
rm -f "$root/etc/resolv.conf"

tar --numeric-owner --xattrs -C "$root" -cf - . | gzip -1 > "$work/agent-base-layer.tar.gz"
rm -rf --one-file-system "$root"
echo "build: layer $(du -m "$work/agent-base-layer.tar.gz" | cut -f1) MB"
