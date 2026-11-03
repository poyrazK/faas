#!/usr/bin/env bash
# scripts/e2e-journeys-schedule.sh — run the customer journey suite on a
# schedule from this Mac (no CI involved).
#
#   scripts/e2e-journeys-schedule.sh run-due     # what the schedule calls
#   scripts/e2e-journeys-schedule.sh install [--hour 3]
#   scripts/e2e-journeys-schedule.sh status
#   scripts/e2e-journeys-schedule.sh uninstall
#
# run-due runs at most one suite:
#   - a smoke run with the matching CLI when a new v* release is published
#     (prod deploys every release, so this is the post-release check);
#   - otherwise a full run once a day, at or after --hour (local time).
# Reports land in ~/.local/state/gregale-e2e/runs/<UTC time>/ and the
# reporter compares each run with the previous one. A macOS notification
# says when a run fails on something not in known_failures.json.
#
# install writes a LaunchAgent that calls run-due every hour while you are
# logged in (the suite uses your `gregale login`, kept in the keychain).
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
STATE_DIR="${GREGALE_E2E_STATE_DIR:-$HOME/.local/state/gregale-e2e}"
RUNS_DIR="$STATE_DIR/runs"
LABEL=dev.gregale.e2e-journeys
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

usage() {
	sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

notify() {
	command -v osascript >/dev/null || return 0
	osascript -e "display notification \"$1\" with title \"Gregale journeys\"" >/dev/null 2>&1 || true
}

latest_release() {
	gh release list --repo poyrazK/faas --limit 20 --json tagName \
		--jq '[.[] | select(.tagName | startswith("v"))][0].tagName'
}

run_suite() {
	local tier="$1" version="$2" out rc=0
	out="$RUNS_DIR/$(date -u +%Y%m%dT%H%M%SZ)-$tier"
	mkdir -p "$out"
	echo "$(date -u +%FT%TZ) starting $tier run with CLI $version -> $out" >>"$STATE_DIR/schedule.log"
	(cd "$REPO_ROOT" && scripts/e2e-journeys.sh --tier "$tier" --cli-version "$version" --out "$out") \
		>"$out/run.log" 2>&1 || rc=$?
	echo "$(date -u +%FT%TZ) $tier run finished rc=$rc" >>"$STATE_DIR/schedule.log"
	if ((rc != 0)); then
		notify "$tier run against $version found new failures: $out/report.md"
	fi
	return 0
}

run_due() {
	local hour=3 version last_release last_full now_day
	while (($#)); do
		case "$1" in
		--hour) hour="$2"; shift 2 ;;
		*) echo "unknown flag: $1" >&2; exit 2 ;;
		esac
	done
	mkdir -p "$RUNS_DIR"
	# One run at a time: an hourly tick must not start a second suite while
	# a full run (~40 min) is still going.
	if ! mkdir "$STATE_DIR/lock" 2>/dev/null; then
		echo "$(date -u +%FT%TZ) skipped: a run is in progress" >>"$STATE_DIR/schedule.log"
		return 0
	fi
	trap 'rmdir "$STATE_DIR/lock"' EXIT
	# Use the toolchain go.mod pins, whatever `go env` says globally.
	GOTOOLCHAIN="$(awk '/^toolchain /{print $2}' "$REPO_ROOT/go.mod")"
	export GOTOOLCHAIN

	version="$(latest_release)" || { echo "cannot list releases" >&2; return 1; }
	last_release="$(cat "$STATE_DIR/last-release" 2>/dev/null || true)"
	if [[ -n "$version" && "$version" != "$last_release" ]]; then
		run_suite smoke "$version"
		echo "$version" >"$STATE_DIR/last-release"
		return 0
	fi
	now_day="$(date +%Y-%m-%d)"
	last_full="$(cat "$STATE_DIR/last-full-day" 2>/dev/null || true)"
	if [[ "$last_full" != "$now_day" && "$(date +%-H)" -ge "$hour" ]]; then
		run_suite full "$version"
		echo "$now_day" >"$STATE_DIR/last-full-day"
	fi
}

install_agent() {
	local hour=3
	while (($#)); do
		case "$1" in
		--hour) hour="$2"; shift 2 ;;
		*) echo "unknown flag: $1" >&2; exit 2 ;;
		esac
	done
	[[ "$hour" =~ ^([0-9]|1[0-9]|2[0-3])$ ]] || { echo "--hour must be 0-23" >&2; exit 2; }
	mkdir -p "$(dirname "$PLIST")" "$STATE_DIR"
	cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>$REPO_ROOT/scripts/e2e-journeys-schedule.sh</string>
		<string>run-due</string>
		<string>--hour</string>
		<string>$hour</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key><string>$PATH</string>
		<key>HOME</key><string>$HOME</string>
	</dict>
	<key>StartInterval</key><integer>3600</integer>
	<key>RunAtLoad</key><false/>
	<key>StandardOutPath</key><string>$STATE_DIR/launchd.log</string>
	<key>StandardErrorPath</key><string>$STATE_DIR/launchd.log</string>
</dict>
</plist>
EOF
	launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
	launchctl bootstrap "gui/$(id -u)" "$PLIST"
	echo "installed $LABEL: hourly check, smoke on each new release, full daily after ${hour}:00"
	echo "reports: $RUNS_DIR"
}

case "${1:-}" in
run-due) shift; run_due "$@" ;;
install) shift; install_agent "$@" ;;
uninstall)
	launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
	rm -f "$PLIST"
	echo "uninstalled $LABEL"
	;;
status)
	if launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1; then
		echo "$LABEL is loaded"
	else
		echo "$LABEL is not installed"
	fi
	echo "last release tested: $(cat "$STATE_DIR/last-release" 2>/dev/null || echo none)"
	echo "last full run day:   $(cat "$STATE_DIR/last-full-day" 2>/dev/null || echo none)"
	tail -n 5 "$STATE_DIR/schedule.log" 2>/dev/null || true
	;;
-h|--help|"") usage ;;
*) echo "unknown command: $1" >&2; usage >&2; exit 2 ;;
esac
