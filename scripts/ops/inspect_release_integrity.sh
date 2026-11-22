#!/usr/bin/env bash
# Read-only: why does `gregalectl release verify` fail on this node?
# Runs as root on a compute node over stdin (ops-release-integrity-inspect).
# Prints the verify failures, the release tree and, for each flagged pid, its
# command name, systemd unit, start time and executable path. It changes
# nothing and prints no environment or command-line arguments.
set -uo pipefail
verify=/usr/local/bin/gregalectl
echo "== host $(hostname) at $(date -u +%FT%TZ)"
echo "== current release: $(readlink -f /opt/faas/current 2>/dev/null)"
echo "== installed releases:"
find /opt/faas/releases -mindepth 1 -maxdepth 1 -printf "%TY-%Tm-%Td %TH:%TM %f\n" 2>/dev/null | sort -r | head -20
echo "== release verify:"
out=$("$verify" release verify 2>&1)
printf '%s\n' "$out" | cut -c1-500
echo "== flagged processes:"
printf '%s\n' "$out" | grep -oE 'pid [0-9]+' | awk '{print $2}' | sort -un | while read -r pid; do
  exe=$(readlink "/proc/$pid/exe" 2>/dev/null)
  comm=$(cat "/proc/$pid/comm" 2>/dev/null)
  started=$(ps -o lstart= -p "$pid" 2>/dev/null | sed 's/^ *//')
  unit=$(grep -oE '[^/]+\.(service|scope)' "/proc/$pid/cgroup" 2>/dev/null | tail -1)
  ppid=$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')
  printf 'pid=%s ppid=%s comm=%s unit=%s started=%s exe=%s\n' "$pid" "$ppid" "$comm" "$unit" "$started" "$exe"
done
echo "== integrity journal (12 h):"
journalctl -t faas-release-integrity --since -12h -o short-iso --no-pager 2>/dev/null | tail -12 | cut -c1-500
