#!/usr/bin/env bash
# Build the agent base (drive0) and the small per-run layer (drive1) for the
# keyless memory experiment, without docker. Run as root on the KVM host.
#
#   build_agent_images.sh WORK GUEST_INIT PROBE_DIR [stage...]
#
# Stages: pull install fidelity images (default: all, in order). The base is
# node:22-alpine (the digest pinned in ../images/agent-base.Dockerfile) with
# the three harnesses and this checkout's guest-init as /sbin/init, so it is
# a shared read-only drive0 as ADR-20261027090010 §2 describes. The layer carries only
# app.json, the probe and the run input.
set -euo pipefail
# systemd units on merged-usr hosts omit /sbin and /bin; Alpine keeps apk there.
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

work=$1 guest_init=$2 probe_dir=$3
shift 3
stages=("$@")
[[ ${#stages[@]} -gt 0 ]] || stages=(pull install fidelity images)

registry=mirror.gcr.io repo=library/node
digest=sha256:76789712cd1ae89a1225eac9077010d68987a423588042dac30446f502f1858c
codex=0.163.0 opencode=1.19.3 claude=2.1.302
express_old=4.21.1 express_new=4.21.2

root=$work/rootfs
mkdir -p "$work"

unmount_root() {
  local m
  for m in "$root/dev" "$root/proc"; do
    if mountpoint -q "$m"; then umount "$m"; fi
  done
  if findmnt -rn -o TARGET | grep -q "^$root/"; then
    echo "build: mounts remain under $root; refusing to continue" >&2
    exit 1
  fi
}
mount_root() {
  mountpoint -q "$root/proc" || mount -t proc proc "$root/proc"
  mountpoint -q "$root/dev" || mount --bind /dev "$root/dev"
  cp /etc/resolv.conf "$root/etc/resolv.conf"
}
trap unmount_root EXIT

for stage in "${stages[@]}"; do
  case $stage in
  pull)
    unmount_root
    [[ ! -e $root ]] || rm -rf --one-file-system "$root"
    python3 -I "$probe_dir/oci_pull.py" "$registry" "$repo" "$digest" "$root"
    ;;
  install)
    mount_root
    # The same steps as agent-base.Dockerfile.
    chroot "$root" /bin/sh -c "apk upgrade --no-cache && \
      apk add --no-cache bash git python3 ripgrep libstdc++ libgcc ca-certificates tini && \
      npm install -g --omit=dev @openai/codex@$codex opencode-ai@$opencode @anthropic-ai/claude-code@$claude && \
      npm cache clean --force && rm -rf /root/.npm /tmp/*"
    chroot "$root" /bin/sh -c 'if id app >/dev/null 2>&1; then :; elif id node >/dev/null 2>&1; then sed -i "s/^node:/app:/" /etc/passwd; else adduser -D -u 1000 app; fi'
    chroot "$root" /bin/sh -c 'codex --version; claude --version; opencode --version'
    unmount_root
    # guest-init boots drive0, so it replaces Alpine's /sbin/init.
    rm -f "$root/sbin/init"
    install -m 0755 "$guest_init" "$root/sbin/init"
    for d in dev overlay proc run sys sys/fs/cgroup tmp etc/faas input work/output; do
      mkdir -p "$root/$d"
    done
    chown -R 1000:1000 "$root/work"
    ;;
  fidelity)
    # Each preset must finish S1 against the fake provider before any VM boots.
    stage_in=$work/fidelity-input
    rm -rf "$stage_in" && mkdir -p "$stage_in"
    "$probe_dir/make_input.sh" "$stage_in" "$express_old" "$express_new" "$root"
    mkdir -p "$root/opt/agentprobe" "$root/input"
    cp -r "$probe_dir/agent_probe.py" "$probe_dir/scripts" "$root/opt/agentprobe/"
    cp -a "$stage_in/." "$root/input/"
    chown -R 1000:1000 "$root/input"
    mount_root
    rc=0
    for preset in ${FIDELITY_PRESETS:-codex claude-code opencode}; do
      chroot --userspec=1000:1000 "$root" /usr/bin/env -i PATH=/usr/local/bin:/usr/bin:/bin \
        AGENT_PROBE_SCRIPTS=/opt/agentprobe/scripts \
        python3 -I /opt/agentprobe/agent_probe.py --root /tmp/fidelity --provider-port 18787 \
        --fidelity "$preset" "${FIDELITY_SCRIPT:-s1}" > "$work/fidelity-$preset.json" || rc=1
      python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); print(sys.argv[2], "ok" if d["ok"] else "FAIL", d["calls"], "calls", d["elapsed_s"], "s", d["peak_tree_rss_kb"]//1024, "MB tree RSS")' \
        "$work/fidelity-$preset.json" "$preset" || true
    done
    unmount_root
    rm -rf --one-file-system "$root/tmp/fidelity" "$root/opt/agentprobe" "$root/input"
    mkdir -p "$root/input"
    [[ $rc -eq 0 ]] || { echo "build: fidelity gate failed" >&2; exit 1; }
    ;;
  images)
    unmount_root
    rm -f "$root/etc/resolv.conf"
    base_mb=$(( $(du -sm "$root" | cut -f1) * 5 / 4 + 256 ))
    truncate -s 0 "$work/agent-base.ext4"
    truncate -s "${base_mb}M" "$work/agent-base.ext4"
    mkfs.ext4 -q -O '^has_journal' -d "$root" -L agent-base -F "$work/agent-base.ext4"
    e2fsck -fn "$work/agent-base.ext4" >/dev/null
    # drive1: the per-run layer. Production app artifacts live below /upper.
    skel=$work/layer-skel
    rm -rf "$skel"
    mkdir -p "$skel/upper/etc/faas" "$skel/upper/opt/agentprobe" "$skel/upper/input" "$skel/upper/tmp"
    cp -r "$probe_dir/agent_probe.py" "$probe_dir/scripts" "$skel/upper/opt/agentprobe/"
    stage_in=$work/fidelity-input
    [[ -d $stage_in ]] || { mkdir -p "$stage_in"; "$probe_dir/make_input.sh" "$stage_in" "$express_old" "$express_new" "$root"; }
    cp -a "$stage_in/." "$skel/upper/input/"
    chown -R 1000:1000 "$skel/upper/input" "$skel/upper/opt/agentprobe"
    printf '%s\n' '{"entrypoint":["/usr/bin/python3","-I","/opt/agentprobe/agent_probe.py"],"port":8080,"working_dir":"/work","env":{"AGENT_PROBE_ROOT":"/tmp/agent-probe","AGENT_PROBE_SCRIPTS":"/opt/agentprobe/scripts","AGENT_PROBE_INPUT":"/input"}}' \
      > "$skel/upper/etc/faas/app.json"
    truncate -s 0 "$work/agent-layer.ext4"
    truncate -s 64M "$work/agent-layer.ext4"
    mkfs.ext4 -q -O '^has_journal' -d "$skel" -L agent-layer -F "$work/agent-layer.ext4"
    e2fsck -fn "$work/agent-layer.ext4" >/dev/null
    chmod 0644 "$work/agent-base.ext4" "$work/agent-layer.ext4"
    ls -la "$work"/agent-*.ext4
    ;;
  *) echo "build: unknown stage $stage" >&2; exit 2 ;;
  esac
done
