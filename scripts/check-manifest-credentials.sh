#!/usr/bin/env bash
# Refuse a deployment manifest that carries a credential.
#
# A release publishes its materialized production manifest as a release
# asset beside the signed bundle, so anything in a topology template is
# public. Credentials never belong in a manifest: they live in
# controller-local files that fields like log_archive.credentials_src or
# object_storage.provider_env_src name by path.
#
# usage: check-manifest-credentials.sh FILE...
#
# Reports file:line and the kind of credential, never the matched text, so
# the refusal itself cannot leak it into a CI log. Comment lines are
# checked too: a commented-out key is still published.
set -euo pipefail

[[ $# -gt 0 ]] || { echo "usage: check-manifest-credentials.sh FILE..." >&2; exit 2; }

# kind|extended regex, matched case-sensitively unless the kind ends in -i.
checks=(
  'private key block|-----BEGIN [A-Z ]*PRIVATE KEY'
  'age identity|AGE-SECRET-KEY-1'
  'AWS access key id|(AKIA|ASIA)[0-9A-Z]{16}'
  'GitHub token|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}'
  'Google service-account key|"?private_key(_id)?"?[[:space:]]*:'
  'chat webhook URL|hooks\.slack\.com/services/|discord(app)?\.com/api/webhooks/'
  'password in a URL|://[^/@:[:space:]]+:[^/@[:space:]]+@'
  'inline secret value-i|^[[:space:]]*(#[[:space:]]*)?(-[[:space:]]*)?[A-Za-z0-9_]*(password|passwd|secret|token|api_key|apikey|access_key|access_key_id|secret_access_key|client_secret)[[:space:]]*:[[:space:]]*[^[:space:]#"'"'"'~{]'
)

found=0
for file in "$@"; do
  [[ -f "$file" ]] || { echo "check-manifest-credentials: not a file: $file" >&2; exit 2; }
  for check in "${checks[@]}"; do
    kind="${check%%|*}"
    regex="${check#*|}"
    flags=(-n -E)
    if [[ "$kind" == *-i ]]; then
      kind="${kind%-i}"
      flags+=(-i)
    fi
    while IFS=: read -r line _; do
      echo "check-manifest-credentials: $file:$line: $kind; move it to a controller-local file and name that file in the manifest" >&2
      found=1
    done < <(grep "${flags[@]}" -- "$regex" "$file" || true)
  done
done
((found == 0)) || exit 1
