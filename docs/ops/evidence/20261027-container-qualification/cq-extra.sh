#!/usr/bin/env bash
# Guest container-environment checks (ADR-20261025111339) that need Linux
# and root but neither KVM nor the acceptance lock: the guest/init package
# tests, and the /tmp overlay metal test in a private mount namespace.
# Usage: sudo setsid nohup bash cq-extra.sh <run-dir> >/dev/null 2>&1 &
set -uo pipefail
RUN="$1"
exec >>"$RUN/extra.log" 2>&1
C=/mnt/e2e-scratch/opsv1-20261017/go
export HOME="$RUN/home" GOPATH="$C" GOMODCACHE="$C/mod" GOCACHE="$C/cache"
export GOTOOLCHAIN="$(awk '/^toolchain /{print $2}' "$RUN/src/go.mod")"
export PATH=/usr/local/go/bin:/usr/bin:/bin TMPDIR="$RUN/tmp" GOFLAGS=-p=1 GOGC=50
mkdir -p "$HOME" "$RUN/tmp"
cd "$RUN/src"
echo "start $(date -u +%FT%TZ) rev $(cat "$RUN/REVISION")"
nice -n 19 go test -count=1 ./guest/init > "$RUN/guest-init.log" 2>&1
echo "GUEST_INIT_EXIT=$? $(grep -c '^--- FAIL' "$RUN/guest-init.log") failed"
tail -5 "$RUN/guest-init.log"
nice -n 19 go test -count=1 -tags metal -run '^TestGuestTmpKeepsImageFiles$' -v ./guest/init > "$RUN/tmp-overlay.log" 2>&1
echo "TMP_OVERLAY_EXIT=$?"
grep -E '^(--- |ok|FAIL|PASS)' "$RUN/tmp-overlay.log"
echo "EXTRA_DONE $(date -u +%FT%TZ)"
