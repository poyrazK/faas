#!/usr/bin/env bash
# Runner-local, pinned SSH transport for the scheduled deployment canary.
# setup never changes the runner's shared ~/.ssh files and prints only fixed
# status codes. run preserves the remote command's output for acceptance logs.
set -euo pipefail

canary_ssh_dir="${RUNNER_TEMP:?RUNNER_TEMP is required}/canary-ssh"

canary_ssh_error() {
  printf '%s\n' "$1" > "$canary_ssh_dir/status"
  echo "::error::canary-ssh: $1" >&2
  return 1
}

canary_ssh_options=(
  -F /dev/null -T -i "$canary_ssh_dir/deploy_key"
  -o BatchMode=yes -o IdentitiesOnly=yes
  -o ConnectTimeout=10 -o ConnectionAttempts=1
  -o ServerAliveInterval=10 -o ServerAliveCountMax=2
  -o StrictHostKeyChecking=yes
  -o "UserKnownHostsFile=$canary_ssh_dir/known_hosts"
  -o GlobalKnownHostsFile=/dev/null -o UpdateHostKeys=no
  -o ForwardAgent=no -o ClearAllForwardings=yes -o LogLevel=ERROR
)

canary_ssh_setup() {
  umask 077
  mkdir -p "$canary_ssh_dir"
  chmod 0700 "$canary_ssh_dir"
  # A failed setup must not leave a previous successful setup usable.
  rm -f "$canary_ssh_dir/ready" "$canary_ssh_dir/known_hosts" "$canary_ssh_dir/deploy_key"
  printf '%s\n' ssh_setup_incomplete > "$canary_ssh_dir/status"
  if [[ -z "${CP_HOST:-}" || -z "${CP_SSH_KEY:-}" ]]; then
    canary_ssh_error ssh_configuration_missing
    return 1
  fi
  if [[ ! "$CP_HOST" =~ ^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$ ]]; then
    canary_ssh_error ssh_host_invalid
    return 1
  fi
  if [[ -z "${COMPUTE_KNOWN_HOSTS:-}" ]]; then
    canary_ssh_error ssh_host_pins_missing
    return 1
  fi
  printf '%s\n' "$CP_SSH_KEY" > "$canary_ssh_dir/deploy_key"
  chmod 0600 "$canary_ssh_dir/deploy_key"
  : > "$canary_ssh_dir/known_hosts"

  local scanned scan_status=0 pinned=0 scanned_host key_type key extra
  # ssh-keyscan's -T is per connection; bound the whole scan as well.
  scanned="$(timeout --signal=TERM --kill-after=5s 15s \
    ssh-keyscan -T 10 -t ed25519,ecdsa,rsa "$CP_HOST" 2>/dev/null)" || scan_status=$?
  if (( scan_status == 124 || scan_status == 137 )); then
    canary_ssh_error ssh_host_scan_timeout
    return 1
  fi
  if [[ -z "$scanned" ]]; then
    canary_ssh_error ssh_host_keys_unavailable
    return 1
  fi
  while read -r scanned_host key_type key extra; do
    [[ "$scanned_host" != \#* && -z "$extra" && -n "$key" ]] || continue
    case "$key_type" in
      ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521) ;;
      *) continue ;;
    esac
    # Normal and hashed known_hosts entries both put the key in fields 2/3.
    # Marker entries (including @revoked) are never positive host-key pins.
    if awk -v t="$key_type" -v k="$key" \
      '$1 !~ /^[#@]/ && $2 == t && $3 == k { found = 1 } END { exit !found }' \
      <<<"$COMPUTE_KNOWN_HOSTS"; then
      printf '%s %s %s\n' "$CP_HOST" "$key_type" "$key" >> "$canary_ssh_dir/known_hosts"
      pinned=1
    fi
  done <<<"$scanned"
  if (( pinned == 0 )); then
    canary_ssh_error ssh_host_key_untrusted
    return 1
  fi
  chmod 0600 "$canary_ssh_dir/known_hosts"

  local error preflight_status=0
  # Authenticate before calling this transport ready. Suppress raw diagnostics
  # but retain enough information to distinguish credentials from connectivity.
  error="$(LC_ALL=C timeout --signal=TERM --kill-after=5s 20s \
    ssh "${canary_ssh_options[@]}" "root@$CP_HOST" true 2>&1 >/dev/null)" || preflight_status=$?
  if (( preflight_status != 0 )); then
    if (( preflight_status == 124 || preflight_status == 137 )); then
      canary_ssh_error ssh_preflight_timeout
    elif [[ "$error" == *"Permission denied"* ]]; then
      canary_ssh_error ssh_authentication_failed
    elif [[ "$error" == *"Host key verification failed"* || "$error" == *"REMOTE HOST IDENTIFICATION HAS CHANGED"* ]]; then
      canary_ssh_error ssh_host_key_untrusted
    else
      canary_ssh_error ssh_preflight_failed
    fi
    return 1
  fi
  printf '%s\n' ssh_ready > "$canary_ssh_dir/status"
  : > "$canary_ssh_dir/ready"
  echo 'canary-ssh: ssh_ready'
}

canary_ssh_run() {
  if [[ ! -f "$canary_ssh_dir/ready" || ! -s "$canary_ssh_dir/known_hosts" || ! -s "$canary_ssh_dir/deploy_key" ]]; then
    echo '::error::canary-ssh: ssh_not_ready' >&2
    return 1
  fi
  ssh "${canary_ssh_options[@]}" "root@${CP_HOST:?CP_HOST is required}" "$@"
}

case "${1:-}" in
  setup) canary_ssh_setup ;;
  run) shift; canary_ssh_run "$@" ;;
  *) echo 'usage: canary_ssh.sh setup|run [remote command]' >&2; exit 2 ;;
esac
