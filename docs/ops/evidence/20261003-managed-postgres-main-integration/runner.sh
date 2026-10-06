#!/usr/bin/env bash
set -Eeuo pipefail
root=/var/tmp/faas-metal-smoke-managed-pg-mega-pr-2cd325e9
user=kucukarslanhuseyinpoyraz_gmail_c
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TMPDIR=/run/mppr-2cd325e9-metal GOCACHE="$root/go-build" GOMODCACHE="$root/go-mod" GOPATH="$root/go-path"
export GOTOOLCHAIN=local GOFLAGS=-p=1 GOMAXPROCS=1 GOGC=10 GOMEMLIMIT=384MiB
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0="$root/source"
mkdir -p "$TMPDIR" "$root/fixtures"
chmod 1777 "$TMPDIR"
cd "$root/source"
sha256sum -c "$root/pinned-source-hashes.sha256" > "$root/pinned-metal-source-match-before.log"

# Full daemon race suite is unprivileged; fcvm coverage is the bounded metal
# selection below. The all-fcvm retry retained an OOM at its 3 GiB string fixture.
runuser -u "$user" --preserve-environment -- /usr/local/bin/go test -race -v -count=1 -timeout=10m ./cmd/vmmd > "$root/pinned-vmmd-linux.log" 2>&1
printf '0\n' > "$root/pinned-vmmd-linux.exit"
printf 'full vmmd Linux race suite passed\n'
cleanup() {
 local rc=$?
 trap - EXIT
 if [ "${metal_locked:-0}" -eq 1 ]; then make leakcheck || rc=1; fi
 exit "$rc"
}
trap cleanup EXIT
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134 FAAS_TEST_FC_VERSION=1.7.0 FAAS_TEST_REFERENCE_SSD=0
for service in faas-vmmd.service faas-builderd.service faas-imaged.service faas-gatewayd-internal.service; do
 if systemctl is-active --quiet "$service"; then printf 'active daemon: %s
' "$service" >&2; exit 1; fi
done
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 1800 9
metal_locked=1
make leakcheck
base="$root/fixtures/base-skeleton"
layer="$root/fixtures/layer-skeleton"
mkdir -p "$base/bin" "$base/sbin" "$base/etc/faas" "$layer/upper/etc/faas" "$layer/upper/tmp"
for d in dev overlay proc run sys sys/fs/cgroup tmp; do mkdir -p "$base/$d"; done
CGO_ENABLED=0 /usr/local/bin/go build -trimpath -buildvcs=false -o "$base/sbin/init" ./guest/init
install -m 0755 /usr/bin/busybox "$base/bin/busybox"
for name in bin/sh bin/ash bin/cat; do ln -sfn /bin/busybox "$base/$name"; done
printf '%s
' '{"entrypoint":["/bin/busybox","httpd","-f","-p","8080","-h","/"],"port":8080}' > "$layer/upper/etc/faas/app.json"
export FAAS_TEST_BASE_ROOTFS="$root/fixtures/hello-base.ext4" FAAS_TEST_LAYER_ROOTFS="$root/fixtures/hello-layer.ext4"
truncate -s 64M "$FAAS_TEST_BASE_ROOTFS"
mkfs.ext4 -q -O '^has_journal' -d "$base" -L faas-metal-smoke -F "$FAAS_TEST_BASE_ROOTFS"
truncate -s 16M "$FAAS_TEST_LAYER_ROOTFS"
mkfs.ext4 -q -O '^has_journal' -d "$layer" -L faas-metal-layer -F "$FAAS_TEST_LAYER_ROOTFS"
chmod 0644 "$FAAS_TEST_BASE_ROOTFS" "$FAAS_TEST_LAYER_ROOTFS"
export RUN_REGEX='^(TestMetalResourceRestartPrepared|TestResourceRestartPrepared|TestMetalResourcePrepared|TestResourcePrepared|TestPreparedNetwork|TestMetalResourceLinks|TestResourceLinks|TestMetalResourcePlacement|TestMetalResourceAssets|TestResourcePlacement|TestResourceAssets|TestMetalResourceJournal|TestResourceJournal|TestMetalRestartQuarantine|TestRestartQuarantine|TestMetalFailureOutbox|TestMetalTeardownRetainsOwnership|TestMetalSchedulerRetainsAccounting|TestMetalAppAdmission|TestAppAdmission|TestFailureReport|TestRecoveredFailureReport|TestLoadConfigResourceJournal)'
make GO=/usr/local/bin/go PKGS='./pkg/fcvm ./pkg/sched ./cmd/vmmd' RUN_ARGS='-timeout=20m -v' test-metal > "$root/pinned-metal.log" 2>&1
printf '0
' > "$root/pinned-metal.exit"
make leakcheck
sha256sum -c "$root/pinned-source-hashes.sha256" > "$root/pinned-metal-source-match-after.log"
printf 'integrated diagnostic passed
'
