#!/usr/bin/env bash
# Run the keyless agent memory experiment on a shared KVM host. Run as root.
#
#   run_memory.sh DIR RUN...     RUN is LABEL:CELLS[:COMPRESSOR], e.g. lz4:A,B,D,E zstd:B
#
# DIR holds agent-mem-bin/ (vmmd, vmmd-jail-helper, fcvm.metal.test) and
# work/agent-{base,layer}.ext4. Every run holds the host acceptance lock. For
# the reclaim cells the host gets memory-only compressed swap for the length
# of the run: zswap in front of a zram device, with memory.zswap.writeback=0
# on faas-tenant.slice, so tenant memory never reaches a disk. Every other
# cgroup gets memory.swap.max=0, so the rest of a shared host keeps the
# no-swap behaviour it had (without this fence, other sessions' idle memory
# was swapped out during the first run). The previous zswap, swap and fence
# state is restored on exit, and leakcheck runs before and after.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

dir=$1
shift
bin=$dir/agent-mem-bin work=$dir/work out=$dir/results
leakcheck=${LEAKCHECK:-$dir/leakcheck.sh}
mkdir -p "$out" "$dir/tmp"

exec 9>/var/lock/faas-builder-acceptance.lock
echo "run: waiting for the acceptance lock"
flock -w "${LOCK_WAIT_S:-3600}" 9 || { echo "run: acceptance lock held" >&2; exit 75; }
for exe in /proc/[0-9]*/exe; do
  target=$(readlink "$exe" 2>/dev/null || true)
  if [[ ${target##*/} == firecracker* ]]; then
    echo "run: another session's Firecracker VMs are running; stopping" >&2
    exit 75
  fi
done
bash "$leakcheck"

z=/sys/module/zswap/parameters
tenant=/sys/fs/cgroup$(systemctl show -P ControlGroup faas-tenant.slice)
saved_enabled=$(cat $z/enabled) saved_comp=$(cat $z/compressor)
saved_pool=$(cat $z/max_pool_percent) saved_shrinker=$(cat $z/shrinker_enabled)
saved_wb=$(cat "$tenant/memory.zswap.writeback")
zram_loaded=0
lsmod | grep -q '^zram ' && zram_loaded=1
zram_ours=0
fence_saved=$dir/swap-fence.saved

fence_swap() {
  [[ -e $fence_saved ]] && return
  : > "$fence_saved"
  local f
  for f in /sys/fs/cgroup/*/memory.swap.max /sys/fs/cgroup/faas.slice/*/memory.swap.max; do
    case $f in */faas.slice/memory.swap.max | */faas-tenant.slice/memory.swap.max) continue ;; esac
    echo "$f $(cat "$f")" >> "$fence_saved"
    echo 0 > "$f"
  done
}

unfence_swap() {
  [[ -e $fence_saved ]] || return 0
  local f v
  while read -r f v; do
    [[ -e $f ]] && echo "$v" > "$f"
  done < "$fence_saved"
  rm -f "$fence_saved"
}

restore() {
  local rc=$?
  set +e
  echo "$saved_enabled" > $z/enabled
  echo "$saved_comp" > $z/compressor
  echo "$saved_pool" > $z/max_pool_percent
  echo "$saved_shrinker" > $z/shrinker_enabled
  if [[ $zram_ours -eq 1 ]]; then
    swapoff /dev/zram0
    echo 1 > /sys/block/zram0/reset
    [[ $zram_loaded -eq 1 ]] || rmmod zram
  fi
  echo "$saved_wb" > "$tenant/memory.zswap.writeback"
  unfence_swap
  echo "run: restored zswap enabled=$(cat $z/enabled) compressor=$(cat $z/compressor) writeback=$(cat "$tenant/memory.zswap.writeback"); swap: $(swapon --noheadings --show=NAME | tr '\n' ' ')"
  bash "$leakcheck" || rc=1
  rm -rf --one-file-system "$dir/tmp"
  exit "$rc"
}
trap restore EXIT HUP INT TERM

open_swap() {
  local comp=$1
  fence_swap
  if [[ $zram_ours -eq 0 ]]; then
    modprobe zram num_devices=1
    if swapon --noheadings --show=NAME | grep -q zram0 || [[ $(cat /sys/block/zram0/disksize) != 0 ]]; then
      echo "run: /dev/zram0 is in use by someone else" >&2
      exit 75
    fi
    echo 4G > /sys/block/zram0/disksize
    mkswap /dev/zram0 > /dev/null
    swapon -p 5 /dev/zram0
    zram_ours=1
  fi
  echo 0 > "$tenant/memory.zswap.writeback"
  echo "$comp" > $z/compressor
  echo 25 > $z/max_pool_percent
  echo N > $z/shrinker_enabled
  echo Y > $z/enabled
  echo "run: zswap $(cat $z/enabled) $(cat $z/compressor); swap: $(swapon --noheadings --show=NAME,SIZE | tr '\n' ' ')"
}

for spec in "$@"; do
  IFS=: read -r label cells comp <<<"$spec"
  open_swap "${comp:-lz4}"
  echo "run: $label cells=$cells start $(date -u +%FT%TZ)"
  env TMPDIR="$dir/tmp" \
    FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134 FAAS_TEST_FC_VERSION=1.7.0 \
    FAAS_TEST_BASE_ROOTFS="$work/agent-base.ext4" FAAS_TEST_LAYER_ROOTFS="$work/agent-layer.ext4" \
    FAAS_TEST_VMMD_BINARY="$bin/vmmd" FAAS_TEST_FREE_PAGE_REPORTING="${FPR:-0}" \
    FAAS_TEST_AGENT_MEMORY=1 FAAS_TEST_AGENT_MEMORY_OUT="$out/$label.jsonl" \
    FAAS_TEST_AGENT_LABEL="$label" FAAS_TEST_AGENT_CELLS="$cells" \
    "$bin/fcvm.metal.test" -test.run '^TestMetalAgentMemory$' -test.v -test.count=1 \
    -test.timeout "${TEST_TIMEOUT:-4h}" > "$out/$label.log" 2>&1 || echo "run: $label FAILED (see $out/$label.log)"
  echo "run: $label done $(date -u +%FT%TZ)"
  bash "$leakcheck"
done
