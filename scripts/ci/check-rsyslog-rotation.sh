#!/usr/bin/env bash
# Exercise the rendered rsyslog policy against GCE's production ownership:
# /var/log is root:syslog 0775 and its files are syslog:adm 0640.
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "rsyslog-rotation-check: run as root so logrotate can honor su/create" >&2
  exit 1
fi
for identity in syslog adm; do
  if ! getent group "${identity}" >/dev/null; then
    echo "rsyslog-rotation-check: missing ${identity} group" >&2
    exit 1
  fi
done
if ! getent passwd syslog >/dev/null; then
  echo "rsyslog-rotation-check: missing syslog user" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
template="${repo_root}/deploy/ansible/roles/host_hardening/templates/rsyslog.logrotate.j2"
test_root="$(mktemp -d /tmp/gregale-rsyslog-rotation.XXXXXX)"
trap 'rm -rf "${test_root}"' EXIT
chmod 0755 "${test_root}"
install -d -o root -g syslog -m 0775 "${test_root}/log"
install -d -o root -g root -m 0755 "${test_root}/state"
install -o syslog -g adm -m 0640 /dev/null "${test_root}/log/syslog"

{
  printf '%s\n' "${test_root}/log/syslog"
  awk '
    /^\{$/ { block = 1 }
    block && /^[[:space:]]*postrotate$/ { scripts = 1; next }
    scripts && /^[[:space:]]*endscript$/ { scripts = 0; next }
    block && !scripts { print }
  ' "${template}" |
    sed \
      -e 's/{{ faas_hardening_rsyslog_max_file_size }}/1/' \
      -e 's/{{ faas_hardening_rsyslog_rotate }}/2/'
} >"${test_root}/policy"

write_sample() {
  runuser -u syslog -- sh -c \
    "dd if=/dev/zero of='${test_root}/log/syslog' bs=1024 count=4 status=none"
}

write_sample
logrotate --debug "${test_root}/policy" >"${test_root}/debug.log" 2>&1
if grep -Fqi 'insecure permissions' "${test_root}/debug.log"; then
  cat "${test_root}/debug.log" >&2
  exit 1
fi
logrotate --force --state "${test_root}/state/status" "${test_root}/policy"
write_sample
logrotate --force --state "${test_root}/state/status" "${test_root}/policy"

for path in "${test_root}/log/syslog" "${test_root}/log/syslog.1"; do
  actual="$(stat -c '%U:%G:%a' "${path}")"
  if [[ "${actual}" != 'syslog:adm:640' ]]; then
    echo "rsyslog-rotation-check: ${path} ownership/mode=${actual}, want syslog:adm:640" >&2
    exit 1
  fi
done
if [[ ! -s "${test_root}/log/syslog.2.gz" ]]; then
  echo "rsyslog-rotation-check: delayed compression output is missing" >&2
  exit 1
fi

echo "rsyslog-rotation-check: OK"
