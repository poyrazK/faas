#!/usr/bin/env bash
# Tests for faas-reload-or-restart with a fake systemctl. Linux (GNU date/stat).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/faas-reload-or-restart"
fails=0

setup() {
  work="$(mktemp -d)"
  state="$work/state"; mkdir -p "$state" "$work/proc/100" "$work/bin" "$work/etc"
  printf 'active' >"$state/ActiveState"
  printf '100' >"$state/MainPID"
  printf 'no' >"$state/NeedDaemonReload"
  printf '{ path=/bin/kill ; argv[]=/bin/kill -USR2 $MAINPID }' >"$state/ExecReload"
  printf 'serving' >"$state/StatusText"
  touch "$work/etc/unit.service" "$work/etc/10-dropin.conf" "$work/etc/env"
  printf '%s' "$work/etc/unit.service" >"$state/FragmentPath"
  printf '%s' "$work/etc/10-dropin.conf" >"$state/DropInPaths"
  printf '%s (ignore_errors=yes)' "$work/etc/env" >"$state/EnvironmentFiles"
  printf '' >"$state/LoadCredential"
  # The unit started after its inputs were written.
  sleep 1
  date '+%a %Y-%m-%d %H:%M:%S %Z' -d '+1 second' >"$state/ActiveEnterTimestamp"
  for exe in "$work/proc/100/exe" "$work/bin/gatewayd-public"; do
    printf '#!/bin/sh\nexit 0\n' >"$exe"; chmod +x "$exe"
  done
  # on_reload: what the fake does when reloaded (successor | failed | rejected | nothing)
  printf 'successor' >"$work/on_reload"
  cat >"$work/systemctl" <<EOF
#!/usr/bin/env bash
echo "\$*" >>"$work/calls"
case "\$1" in
  show) cat "$state/\${3#--property=}" 2>/dev/null ;;
  reload)
    case "\$(cat "$work/on_reload")" in
      successor) printf '200' >"$state/MainPID" ;;
      failed) printf 'hand-off failed at ready: timeout' >"$state/StatusText" ;;
      rejected) printf 'hand-off failed at checks: rejected by pre-traffic check' >"$state/StatusText" ;;
    esac ;;
esac
EOF
  chmod +x "$work/systemctl"
  : >"$work/calls"
}

run() {
  FAAS_SYSTEMCTL="$work/systemctl" FAAS_RELOAD_PROC="$work/proc" FAAS_RELOAD_BIN_DIR="$work/bin" \
    FAAS_RELOAD_WAIT_SECONDS=2 FAAS_RELOAD_POLL_SECONDS=0.05 \
    bash "$script" faas-gatewayd-public-edge.service gatewayd-public >"$work/out" 2>&1
}

expect() { # name, want exit, want verb (reload|try-restart|none), want no other verb
  local name="$1" want_exit="$2" want="$3" got=0
  run || got=$?
  local verbs; verbs="$(grep -oE '^(reload|try-restart)' "$work/calls" | tr '\n' ' ' || true)"
  if [[ "$got" != "$want_exit" || "$verbs" != "$want" ]]; then
    echo "FAIL $name: exit=$got verbs='$verbs' want exit=$want_exit verbs='$want'"; sed 's/^/    /' "$work/out"
    fails=$((fails + 1))
  else
    echo "ok   $name"
  fi
  rm -rf "$work"
}

setup; expect "clean unit hands off" 0 "reload "
setup; printf 'inactive' >"$state/ActiveState"; expect "inactive unit is left alone" 0 ""
setup; printf 'yes' >"$state/NeedDaemonReload"; expect "unloaded unit change restarts" 0 "try-restart "
setup; touch -d '+5 seconds' "$work/etc/10-dropin.conf"; expect "newer drop-in restarts" 0 "try-restart "
setup; touch -d '+5 seconds' "$work/etc/env"; expect "newer env file restarts" 0 "try-restart "
setup; printf '#!/bin/sh\nexit 1\n' >"$work/bin/gatewayd-public"; expect "new binary without hand-off restarts" 0 "try-restart "
setup; printf '{ path=/bin/kill ; argv[]=/bin/kill -HUP }' >"$state/ExecReload"; expect "no USR2 reload restarts" 0 "try-restart "
setup; printf 'failed' >"$work/on_reload"; expect "failed hand-off falls back to restart" 0 "reload try-restart "
setup; printf 'nothing' >"$work/on_reload"; expect "stalled hand-off falls back to restart" 0 "reload try-restart "
setup; printf 'rejected' >"$work/on_reload"; expect "rejected binary is not restarted" 1 "reload "

if (( fails )); then
  echo "$fails failing case(s)"; exit 1
fi
echo "all faas-reload-or-restart cases pass"
