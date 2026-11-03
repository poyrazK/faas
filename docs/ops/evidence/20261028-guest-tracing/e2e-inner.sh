#!/usr/bin/env bash
# Body of an e2e run, called by e2e-test.sh while it holds the
# host lock. With "private" it runs inside `unshare --mount` and mounts a fresh
# tmpfs over /srv/fc/jail, so another session's native journal is left alone.
set -Eeuo pipefail
W="$1"
MODE="${2:-}"
trap 'echo "INNER_FAILED line $LINENO rc=$?"' ERR
if [ "$MODE" = private ]; then
  mount -t tmpfs -o mode=1700,size=6g tmpfs /srv/fc/jail
fi
SNAP="$W/snap"
rm -rf "$SNAP"
install -d -o root -g faas -m 2770 "$SNAP"
mount --bind "$SNAP" /srv/fc/snap
JAILS=/srv/fc/jail/firecracker
mkdir -p "$JAILS"
{ ls "$JAILS" 2>/dev/null || true; } | sort > "$W/jails-before"
clean_new_vms() {
  { ls "$JAILS" 2>/dev/null || true; } | sort > "$W/jails-after"
  for id in $(comm -13 "$W/jails-before" "$W/jails-after"); do
    echo "cleaning leaked VM $id"
    for p in $(pgrep -f firecracker); do
      if tr "\\0" " " <"/proc/$p/cmdline" 2>/dev/null | grep -q -- "--id $id "; then kill -9 "$p" 2>/dev/null || true; fi
    done
    sleep 1
    for m in $(findmnt -rn -o TARGET | grep "^$JAILS/$id/" | sort -r); do umount "$m" 2>/dev/null || umount -l "$m" 2>/dev/null || true; done
    ip netns del "fc-$id" 2>/dev/null || true
    rm -rf "${JAILS:?}/$id"
  done
}
cleanup() {
  pkill -TERM -f "^$W/bin/" 2>/dev/null || true
  sleep 5
  pkill -KILL -f "^$W/bin/" 2>/dev/null || true
  clean_new_vms
  umount /srv/fc/snap 2>/dev/null || umount -l /srv/fc/snap 2>/dev/null || true
}
trap cleanup EXIT
echo "== leakcheck before"
bash "$W/leakcheck.sh" || echo "LEAKCHECK_BEFORE_FAILED"
set +e
cd "$W/src/cmd/e2e"
"$W/e2e.test" -test.run "$E2E_RUN" -test.v -test.timeout 60m -test.count 1
rc=$?
cd "$W"
set -e
echo "TEST_EXIT=$rc"
echo "== leakcheck after"
if bash "$W/leakcheck.sh"; then echo "LEAKCHECK_AFTER=clean"; else echo "LEAKCHECK_AFTER=failed"; fi
echo "done $(date -u +%FT%TZ)"
