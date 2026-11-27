#!/usr/bin/env bash
# Put an OVH Object Storage key where a host reads it, without the secret
# ever reaching a command line, a log or the terminal.
#
# usage:
#   install-s3-key.sh storage-env --ssh '<ssh command>' --bucket NAME
#       [--fallback gcs] [--delete-mode defer] [--file /etc/faas/storage.env]
#   install-s3-key.sh rclone --ssh '<ssh command>' --bucket NAME
#       [--remote offhostbox] [--file /etc/faas/secrets/storage-box/rclone.conf]
#   install-s3-key.sh log-archive --out PATH
# common options:
#   --endpoint URL   default https://s3.bhs.io.cloud.ovh.net
#   --region NAME    default bhs
#   --dry-run        print what would change (key names only) and write nothing
#
# storage-env sets the FAAS_S3_* lines of storage.env (root:faas 0440,
# docs/runbooks/GcpToOvhMigration.md phase 1 step 4) and keeps every other
# line. rclone writes the [offhostbox] section of the backup rclone.conf
# (root 0400, phase 1 step 8) and keeps every other section. log-archive
# writes the controller-local JSON that log_archive.credentials_src names
# (0600, this machine).
#
# --ssh is how to reach the host as a user with passwordless sudo, for
# example 'ssh ubuntu@203.0.113.10' or
# 'gcloud compute ssh gregale-compute-1 --zone us-east1-b --tunnel-through-iap --'.
# The key pair is read from this terminal (the secret without echo) and
# travels on the SSH session's stdin; the program that merges it into the
# file runs as root on the host and prints key names only. The previous file
# is kept beside it as a root-only .bak-<time> copy. Restarting the daemons
# that read the file is a separate step.
set -euo pipefail

die() { echo "install-s3-key: $*" >&2; exit 2; }
usage() { sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }

[[ $# -ge 1 ]] || usage
mode=$1
shift
case "$mode" in storage-env | rclone | log-archive) ;; -h | --help) usage ;; *) die "unknown mode $mode" ;; esac

ssh_cmd="" bucket="" endpoint="https://s3.bhs.io.cloud.ovh.net" region="bhs"
fallback="" delete_mode="" file="" remote="offhostbox" out="" dry=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ssh) ssh_cmd=${2:?}; shift 2 ;;
    --bucket) bucket=${2:?}; shift 2 ;;
    --endpoint) endpoint=${2:?}; shift 2 ;;
    --region) region=${2:?}; shift 2 ;;
    --fallback) fallback=${2:?}; shift 2 ;;
    --delete-mode) delete_mode=${2:?}; shift 2 ;;
    --file) file=${2:?}; shift 2 ;;
    --remote) remote=${2:?}; shift 2 ;;
    --out) out=${2:?}; shift 2 ;;
    --dry-run) dry=1; shift ;;
    -h | --help) usage ;;
    *) die "unknown option $1" ;;
  esac
done

# Every value that reaches the host is checked against a plain charset, so
# none needs quoting in an env file, an rclone section or JSON.
plain() { [[ $2 =~ ^[A-Za-z0-9._:/+=-]+$ ]] || die "$1 has characters outside [A-Za-z0-9._:/+=-]"; }
[[ $endpoint =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?/?$ ]] || die "--endpoint must be https://host[:port]"
plain --region "$region"
if [[ $mode != log-archive ]]; then
  [[ -n $ssh_cmd ]] || die "--ssh is required"
  [[ $bucket =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ]] || die "--bucket must be an S3 bucket name"
  [[ -z $fallback || $fallback == gcs ]] || die "--fallback takes only gcs"
  [[ -z $delete_mode || $delete_mode == defer ]] || die "--delete-mode takes only defer"
  [[ $remote =~ ^[a-z][a-z0-9_-]*$ ]] || die "--remote must be a plain rclone remote name"
else
  [[ -n $out ]] || die "--out is required"
fi

read -r -p "access key id: " key_id
read -r -s -p "secret key (not shown): " secret
echo
plain "the access key id" "$key_id"
plain "the secret key" "$secret"
((${#key_id} >= 8 && ${#secret} >= 16)) || die "the key pair looks truncated"

if [[ $mode == log-archive ]]; then
  if ((dry)); then
    echo "would write $out: key_id, secret (mode 0600)"
    exit 0
  fi
  [[ ! -e $out ]] || die "$out exists; move it aside first"
  (umask 077 && printf '{"key_id": "%s", "secret": "%s"}\n' "$key_id" "$secret" >"$out")
  echo "wrote $out: key_id, secret (mode 0600)"
  exit 0
fi

# The merge program runs as root on the host. It is passed base64-encoded on
# the command line (it holds no secret); the values arrive on stdin.
read -r -d '' merge_py <<'PY' || true
import json, os, pwd, grp, re, sys, time
op, path, owner, group, perm, dry = sys.argv[1:7]
data = json.load(sys.stdin)
try:
    with open(path) as f:
        old = f.read().splitlines()
except FileNotFoundError:
    old = None
lines = old or []
if op == "env":
    sets = data["set"]
    key = re.compile(r"^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=")
    out = [l for l in lines if not (key.match(l) and key.match(l).group(1) in sets)]
    out += ["%s=%s" % kv for kv in sets.items()]
    summary = ", ".join(sets)
else:
    names = [s["name"] for s in data["sections"]]
    out, skip = [], False
    for l in lines:
        m = re.match(r"^\s*\[(.+?)\]\s*$", l)
        if m:
            skip = m.group(1) in names
        if not skip:
            out.append(l)
    while out and not out[-1].strip():
        out.pop()
    for s in data["sections"]:
        out += ([""] if out else []) + ["[%s]" % s["name"]] + ["%s = %s" % kv for kv in s["body"].items()]
    summary = "; ".join("[%s] %s" % (s["name"], ", ".join(s["body"])) for s in data["sections"])
print("%s %s (%s:%s %s): %s" % ("would write" if dry == "1" else "writing", path, owner, group, perm, summary))
if dry == "1":
    sys.exit(0)
uid, gid = pwd.getpwnam(owner).pw_uid, grp.getgrnam(group).gr_gid
parts, d = [], os.path.dirname(path)
while d and not os.path.isdir(d):
    parts.append(d)
    d = os.path.dirname(d)
for d in reversed(parts):
    os.mkdir(d, 0o700)
    os.chown(d, 0, 0)
    print("created %s (root 0700; bootstrap sets its final mode)" % d)
if old is not None:
    bak = "%s.bak-%s-%d" % (path, time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()), os.getpid())
    fd = os.open(bak, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o400)
    with os.fdopen(fd, "w") as f, open(path) as src:
        f.write(src.read())
    os.chown(bak, 0, 0)
    print("previous file kept at %s (root 0400)" % bak)
tmp = path + ".install-tmp"
fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as f:
    f.write("\n".join(out) + "\n")
    f.flush()
    os.fsync(f.fileno())
os.chown(tmp, uid, gid)
os.chmod(tmp, int(perm, 8))
os.replace(tmp, path)
PY
prog=$(printf '%s' "$merge_py" | base64 | tr -d '\n')

if [[ $mode == storage-env ]]; then
  file=${file:-/etc/faas/storage.env}
  op=env owner=root group=faas perm=0440
  payload=$(printf '{"set": {"FAAS_STORAGE_BACKEND": "s3", "FAAS_S3_BUCKET": "%s", "FAAS_S3_ENDPOINT": "%s", "FAAS_S3_REGION": "%s", "FAAS_S3_PATH_STYLE": "0", "FAAS_S3_ACCESS_KEY_ID": "%s", "FAAS_S3_SECRET_ACCESS_KEY": "%s"' \
    "$bucket" "${endpoint%/}" "$region" "$key_id" "$secret")
  [[ -z $fallback ]] || payload+=', "FAAS_STORAGE_FALLBACK_BACKEND": "gcs"'
  [[ -z $delete_mode ]] || payload+=', "FAAS_STORAGE_DELETE_MODE": "defer"'
  payload+='}}'
else
  file=${file:-/etc/faas/secrets/storage-box/rclone.conf}
  op=ini owner=root group=root perm=0400
  # The backup paths are <remote>:faas-pg-basebackup/..., so the remote's
  # root is the bucket: an alias in front of the s3 remote that holds the key.
  payload=$(printf '{"sections": [{"name": "%s-s3", "body": {"type": "s3", "provider": "Other", "endpoint": "%s", "region": "%s", "access_key_id": "%s", "secret_access_key": "%s", "no_check_bucket": "true"}}, {"name": "%s", "body": {"type": "alias", "remote": "%s-s3:%s"}}]}' \
    "$remote" "${endpoint%/}" "$region" "$key_id" "$secret" "$remote" "$remote" "$bucket")
fi
[[ $file =~ ^/[A-Za-z0-9._/-]+$ && $file != *..* ]] || die "--file must be a plain absolute path"

read -r -a ssh_argv <<<"$ssh_cmd"
remote_cmd="sudo -n python3 -c 'import base64,sys;exec(base64.b64decode(sys.argv.pop(1)))' $prog $op $file $owner $group $perm $dry"
printf '%s' "$payload" | "${ssh_argv[@]}" "$remote_cmd"
unset secret payload
