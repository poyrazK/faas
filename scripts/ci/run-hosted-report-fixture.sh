#!/usr/bin/env bash
# Sourced by the guarded Run acceptance runner after initial leakcheck.
hosted_report_postgres_running=0
hosted_report_ip_owned=0
hosted_report_fs_mounted=0

cleanup_hosted_report() {
  local status=0
  if [[ $hosted_report_postgres_running == 1 ]]; then
    runuser -u postgres -- "$hosted_report_pg_bin/pg_ctl" -D "$hosted_report_pg_dir/data" -m fast -t 30 -w stop >>"$output/fixture.log" 2>&1 || status=1
  fi
  if [[ $hosted_report_ip_owned == 1 ]]; then
    ip address del "$FAAS_TEST_REPORT_GUEST_IP/32" dev lo || status=1
  fi
  if [[ $hosted_report_fs_mounted == 1 ]]; then
    umount "$hosted_report_fs_dir" >>"$output/fixture.log" 2>&1 || status=1
  fi
  if ((status == 0)) && [[ ${hosted_report_pg_dir:-} =~ ^/var/tmp/gregale-hosted-report-db\.[A-Za-z0-9]+$ ]]; then
    rm -rf --one-file-system "$hosted_report_pg_dir" || status=1
  fi
  return "$status"
}

prepare_report_bundle() {
  : "${FAAS_TEST_REPORT_AGENT_NODE:?select the pinned Node 24 executable}"
  : "${FAAS_TEST_REPORT_DEPENDENCIES:?select the verified lockfile-install archive}"
  : "${FAAS_TEST_REPORT_DEPENDENCIES_SHA256:?select the exact dependency archive hash}"
  [[ $FAAS_TEST_REPORT_DEPENDENCIES_SHA256 =~ ^[0-9a-f]{64}$ ]] || die 'invalid locked dependency archive hash'
  [[ $(sha256sum "$FAAS_TEST_REPORT_DEPENDENCIES" | cut -d' ' -f1) == "$FAAS_TEST_REPORT_DEPENDENCIES_SHA256" ]] || die 'locked dependency archive changed'
  [[ $("$FAAS_TEST_REPORT_AGENT_NODE" --version) == v24.* ]] || die 'Report Agent fixture needs Node 24'
  export FAAS_TEST_REPORT_BUNDLE="$stage/work/report-agent"
  export FAAS_TEST_REPORT_ARCHIVE="$stage/work/report-agent.tar.gz"
  export FAAS_TEST_REPORT_MEASUREMENTS="$output/measurements.json"
  "$FAAS_TEST_REPORT_AGENT_NODE" examples/gregale-run/scripts/hosted-pilot.mjs "$FAAS_TEST_REPORT_BUNDLE"
  tar -C "$FAAS_TEST_REPORT_BUNDLE" -czf "$FAAS_TEST_REPORT_ARCHIVE" .
  tar -C "$FAAS_TEST_REPORT_BUNDLE" --no-same-owner --no-same-permissions -xf "$FAAS_TEST_REPORT_DEPENDENCIES"
  sha256sum "$FAAS_TEST_REPORT_AGENT_NODE" "$FAAS_TEST_REPORT_ARCHIVE" "$FAAS_TEST_REPORT_DEPENDENCIES" "$FAAS_TEST_EXECUTION_BASE" \
    "$FAAS_TEST_KERNEL" "$(command -v firecracker)" "$(command -v jailer)" "$stage"/bin/* >"$output/artifacts.sha256"
}

prepare_hosted_report() {
  [[ $(df -B1 --output=avail "$stage/work" | tail -1) -ge $((48*1024*1024*1024)) ]] || die 'hosted builder and reflink scratch require 48 GiB free'
  [[ $(free -m | awk '/^Mem:/{print $7}') -ge 6144 ]] || die 'hosted build requires at least 6 GiB available memory'
  [[ ${FAAS_RUN_NATIVE_PRIVATE_MOUNT_NS:-} == 1 ]] || die 'hosted scratch requires the private mount namespace'
  command -v mkfs.xfs >/dev/null || die 'mkfs.xfs is required for hosted reflink scratch'
  # Function layers are logically 2 GiB. Keep their storage and the jail on
  # the same reflink filesystem so the normal wake path can clone them.
  local scratch_file="$stage/work/hosted-report.xfs"
  hosted_report_fs_dir="$stage/work/hosted-fs"
  mkdir -m 0700 "$hosted_report_fs_dir"
  truncate -s 48G "$scratch_file"
  mkfs.xfs -m reflink=1 "$scratch_file" >>"$output/fixture.log" 2>&1
  mount -t xfs -o loop,nosuid "$scratch_file" "$hosted_report_fs_dir" >>"$output/fixture.log" 2>&1
  hosted_report_fs_mounted=1
  export TMPDIR="$hosted_report_fs_dir"
  export FAAS_TEST_RUN_FIXTURE_ROOT="$TMPDIR/source-fixture"
  printf '%s\n' hosted-report-reflink >"$TMPDIR/reflink-source"
  cp --reflink=always "$TMPDIR/reflink-source" "$TMPDIR/reflink-copy"
  rm "$TMPDIR/reflink-source" "$TMPDIR/reflink-copy"
  export FAAS_TEST_REPORT_NODE24_RUNNER="$stage/bin/node24-runner"
  CGO_ENABLED=0 "$FAAS_RUN_LIFECYCLE_GO" build -ldflags=-w -o "$FAAS_TEST_REPORT_NODE24_RUNNER" ./guest/runners/node24 >>"$output/build.log" 2>&1
  CGO_ENABLED=0 "$FAAS_RUN_LIFECYCLE_GO" build -tags metal -ldflags=-w -o "$stage/bin/realtimed" ./cmd/realtimed >>"$output/build.log" 2>&1
  chmod 0755 "$FAAS_TEST_REPORT_NODE24_RUNNER"

  prepare_report_bundle
  export FAAS_TEST_REPORT_BASE_DIR="$stage/work/report-base-assets"
  mkdir -p "$FAAS_TEST_REPORT_BASE_DIR/base" "$FAAS_TEST_REPORT_BASE_DIR/scans"
  local base
  for base in base-amd64 runner-builder-amd64 runner-node24-amd64 runner-base-debian-parent-amd64; do
    cp --reflink=auto --sparse=always /srv/fc/base/"$base".ext4{,.content,.digest} "$FAAS_TEST_REPORT_BASE_DIR/base/"
    cp "/srv/fc/scans/$base.ext4.scan.json" "$FAAS_TEST_REPORT_BASE_DIR/scans/"
  done
  hosted_base_ref() {
    python3 - "$FAAS_TEST_REPORT_BASE_DIR/scans/$1.ext4.scan.json" <<'PY'
import json,re,sys
image=json.load(open(sys.argv[1]))["image"]
if not re.fullmatch(r"[a-zA-Z0-9._:/-]+@sha256:[0-9a-f]{64}", image):
    raise SystemExit("fixture image must be digest-pinned")
print(image)
PY
  }
  FAAS_TEST_REPORT_BUILDER_REF=$(hosted_base_ref runner-builder-amd64)
  FAAS_TEST_REPORT_NODE24_REF=$(hosted_base_ref runner-node24-amd64)
  FAAS_TEST_REPORT_DEBIAN_PARENT_REF=$(hosted_base_ref runner-base-debian-parent-amd64)
  FAAS_TEST_REPORT_MINIMAL_REF=$(hosted_base_ref base-amd64)
  export FAAS_TEST_REPORT_BUILDER_REF FAAS_TEST_REPORT_NODE24_REF FAAS_TEST_REPORT_DEBIAN_PARENT_REF FAAS_TEST_REPORT_MINIMAL_REF
  sha256sum "$FAAS_TEST_REPORT_BASE_DIR"/base/*.ext4 >>"$output/artifacts.sha256"
  prepare_report_database guest
}

prepare_report_database() {
  # Use a separate private PostgreSQL instance for the application tables.
  hosted_report_pg_bin=$(pg_config --bindir)
  [[ -x $hosted_report_pg_bin/initdb ]] || die 'private PostgreSQL tools are missing'
  hosted_report_pg_dir=$(mktemp -d /var/tmp/gregale-hosted-report-db.XXXXXXXX)
  chown postgres:postgres "$hosted_report_pg_dir"
  local pg_password
  pg_password=$(openssl rand -hex 24)
  printf '%s\n' "$pg_password" >"$hosted_report_pg_dir/password"
  chown postgres:postgres "$hosted_report_pg_dir/password"
  runuser -u postgres -- "$hosted_report_pg_bin/initdb" -A scram-sha-256 -U report_agent --pwfile="$hosted_report_pg_dir/password" \
    -D "$hosted_report_pg_dir/data" >>"$output/fixture.log" 2>&1
  runuser -u postgres -- "$hosted_report_pg_bin/pg_ctl" -D "$hosted_report_pg_dir/data" -w -l "$hosted_report_pg_dir/postgres.log" \
    -o "-c listen_addresses= -k $hosted_report_pg_dir -p 55471" start 9>&- >>"$output/fixture.log" 2>&1
  hosted_report_postgres_running=1
  export FAAS_TEST_REPORT_DB_SOCKET="$hosted_report_pg_dir/.s.PGSQL.55471"
  export FAAS_TEST_REPORT_DB_DSN="postgresql://report_agent:$pg_password@localhost:55471/postgres?host=$hosted_report_pg_dir&sslmode=disable"
  if [[ ${1:-} == guest ]]; then
    export FAAS_TEST_REPORT_GUEST_IP=198.18.0.254
    export FAAS_TEST_REPORT_GUEST_DB_DSN="postgresql://report_agent:$pg_password@$FAAS_TEST_REPORT_GUEST_IP:443/postgres?sslmode=disable"
    [[ -z $(ip -4 address show to "$FAAS_TEST_REPORT_GUEST_IP/32") ]] || die 'the disposable guest address is already owned'
    ip address add "$FAAS_TEST_REPORT_GUEST_IP/32" dev lo
    hosted_report_ip_owned=1
  fi
}
