#!/usr/bin/env bash
# Tests for install-s3-key.sh against a fake ssh and sudo. No network, no
# root: a sitecustomize stub turns the merge program's chown into a no-op.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script="$here/install-s3-key.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/py"
cat >"$work/bin/fakessh" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$@" >>"$ARGV_LOG"
shift # the host
exec sh -c "$*"
SH
cat >"$work/bin/sudo" <<'SH'
#!/bin/sh
[ "$1" = -n ] && shift
exec "$@"
SH
cat >"$work/py/sitecustomize.py" <<'PY'
import collections, grp, os, pwd
os.chown = lambda *a, **k: None
pwd.getpwnam = lambda n: collections.namedtuple("P", "pw_uid")(os.getuid())
grp.getgrnam = lambda n: collections.namedtuple("G", "gr_gid")(os.getgid())
PY
chmod +x "$work/bin/fakessh" "$work/bin/sudo"
export PATH="$work/bin:$PATH" PYTHONPATH="$work/py" ARGV_LOG="$work/argv.log"

key_id=TESTKEYID0123456
secret=TestSecret/0123456789abcdef+=
fail() { echo "FAIL: $*" >&2; exit 1; }
# GNU stat first: on Linux, stat -f is filesystem status and can succeed.
mode_of() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
run() { printf '%s\n%s\n' "$key_id" "$secret" | "$script" "$@" >"$work/out" 2>&1; }
no_secret_leak() {
  ! grep -q "$secret" "$work/out" || fail "$1: secret in output"
  ! grep -q "$secret" "$ARGV_LOG" || fail "$1: secret on the ssh command line"
}

# storage-env: replaces the S3 lines and the backend, keeps everything else.
env_file="$work/storage.env"
cat >"$env_file" <<'ENV'
# shared storage
FAAS_STORAGE_BACKEND=gcs
FAAS_GCS_BUCKET=gregale-artifacts-x
FAAS_S3_ACCESS_KEY_ID=OLDKEY
FAAS_STORAGE_SPARSE_EXTENTS=1
ENV
run storage-env --ssh "fakessh host" --bucket gregale-prod-artifacts --fallback gcs --delete-mode defer --file "$env_file"
no_secret_leak storage-env
grep -qx 'FAAS_STORAGE_BACKEND=s3' "$env_file" || fail "backend not s3"
[[ $(grep -c '^FAAS_STORAGE_BACKEND=' "$env_file") == 1 ]] || fail "backend line duplicated"
grep -qx 'FAAS_GCS_BUCKET=gregale-artifacts-x' "$env_file" || fail "GCS bucket line lost"
grep -qx 'FAAS_STORAGE_SPARSE_EXTENTS=1' "$env_file" || fail "unrelated line lost"
grep -qx '# shared storage' "$env_file" || fail "comment lost"
grep -qx "FAAS_S3_ACCESS_KEY_ID=$key_id" "$env_file" || fail "key id not written"
grep -qx "FAAS_S3_SECRET_ACCESS_KEY=$secret" "$env_file" || fail "secret not written"
! grep -q OLDKEY "$env_file" || fail "old key kept"
grep -qx 'FAAS_STORAGE_FALLBACK_BACKEND=gcs' "$env_file" || fail "fallback missing"
grep -qx 'FAAS_STORAGE_DELETE_MODE=defer' "$env_file" || fail "delete mode missing"
grep -qx 'FAAS_S3_ENDPOINT=https://s3.bhs.io.cloud.ovh.net' "$env_file" || fail "endpoint missing"
compgen -G "$env_file.bak-*" >/dev/null || fail "no backup of the previous file"
grep -q OLDKEY "$env_file".bak-* || fail "backup is not the previous file"
[[ $(mode_of "$env_file") == 440 ]] || fail "storage.env mode not 0440"

# rclone: replaces a stale offhostbox, keeps other remotes, one write.
conf="$work/secrets/storage-box/rclone.conf"
mkdir -p "$(dirname "$conf")"
cat >"$conf" <<'INI'
[other]
type = local

[offhostbox]
type = gcs
bucket_policy_only = true
INI
run rclone --ssh "fakessh host" --bucket gregale-prod-pg-backups --file "$conf"
no_secret_leak rclone
grep -qx '\[other\]' "$conf" || fail "other remote lost"
! grep -q 'type = gcs' "$conf" || fail "stale offhostbox kept"
grep -qx '\[offhostbox\]' "$conf" || fail "alias section missing"
grep -qx 'remote = offhostbox-s3:gregale-prod-pg-backups' "$conf" || fail "alias does not root at the bucket"
grep -qx '\[offhostbox-s3\]' "$conf" || fail "s3 section missing"
grep -qx "secret_access_key = $secret" "$conf" || fail "secret not in rclone.conf"
grep -qx 'no_check_bucket = true' "$conf" || fail "no_check_bucket missing"
[[ $(compgen -G "$conf.bak-*" | wc -l | tr -d ' ') == 1 ]] || fail "want exactly one backup"
[[ $(mode_of "$conf") == 400 ]] || fail "rclone.conf mode not 0400"

# rclone into a directory that does not exist yet (before bootstrap).
fresh="$work/new/secrets/storage-box/rclone.conf"
run rclone --ssh "fakessh host" --bucket gregale-prod-pg-backups --file "$fresh"
[[ -f $fresh ]] || fail "fresh rclone.conf not created"
[[ $(mode_of "$(dirname "$fresh")") == 700 ]] || fail "created dir not 0700"

# --dry-run writes nothing.
before=$(cat "$env_file")
run storage-env --ssh "fakessh host" --bucket gregale-prod-artifacts --file "$env_file" --dry-run
grep -q '^would write' "$work/out" || fail "dry run did not report"
[[ $(cat "$env_file") == "$before" ]] || fail "dry run changed the file"
no_secret_leak dry-run

# log-archive: local JSON, 0600, never overwrites.
json="$work/log-archive-s3.json"
run log-archive --out "$json"
grep -q "\"key_id\": \"$key_id\"" "$json" || fail "log archive key id missing"
[[ $(mode_of "$json") == 600 ]] || fail "log archive json not 0600"
! run log-archive --out "$json" || fail "log-archive overwrote an existing file"

# A value outside the plain charset is refused before anything is sent.
: >"$ARGV_LOG"
if printf '%s\n%s\n' "$key_id" 'bad"secret0123456' | "$script" storage-env --ssh "fakessh host" --bucket gregale-prod-artifacts --file "$env_file" >"$work/out" 2>&1; then
  fail "a quote in the secret was accepted"
fi
[[ ! -s $ARGV_LOG ]] || fail "refused input still reached the host"

echo "install-s3-key: all tests passed"
