#!/usr/bin/env bash
set -Eeuo pipefail
root=/var/tmp/faas-metal-smoke-managed-pg-prepared-handoff-6ac4ffcb
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TMPDIR=/run/mph-6ac4ffcb-checks GOFLAGS=-p=1 GOMAXPROCS=1 GOGC=50 GOMEMLIMIT=2GiB
export GOCACHE="$root/go-build" GOMODCACHE="$root/go-mod" GOPATH="$root/go-path"
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 1800 9
printf '%s  %s\n' 'fd0ae03771fce17178bc68f6a90843617849f0fb947e80712b42b7b68871c389' "$root/renderer-source.tar.gz" | sha256sum -c -
tar -xzf "$root/renderer-source.tar.gz" -C "$root/source"
cd "$root/source"
make leakcheck
cleanup() {
 local rc=$?
 trap - EXIT
 if ! make -C "$root/source" leakcheck; then rc=1; fi
 printf '%s\n' "$rc" > "$root/checks.exit"
 exit "$rc"
}
trap cleanup EXIT
make GO=/usr/local/bin/go egress-check > "$root/egress-check-final.log" 2>&1
/usr/local/bin/go run ./cmd/deployctl/ check > "$root/generate-check.log" 2>&1
sha256sum -c "$root/source-hashes.sha256" > "$root/source-runtime-match.log"
sha256sum cmd/faas-nft-render/main.go > "$root/renderer-runtime.sha256"
printf 'supplemental checks: PASS\n'
