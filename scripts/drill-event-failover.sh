#!/bin/sh
# Event failover drill (ADR-20261029093000): a disposable PostgreSQL primary
# and its streaming standby, an event load at the target rate, a crash of the
# primary (immediate stop, no checkpoint), promotion of the standby, and a
# report of what survived and how routing resumed. Nothing outside the work
# directory is touched.
set -eu
PG_BIN=${PG_BIN:-$(dirname "$(command -v pg_ctl)")}
GO=${GO:-go}
PRIMARY_PORT=${FAAS_EVENT_DRILL_PRIMARY_PORT:-55491}
STANDBY_PORT=${FAAS_EVENT_DRILL_STANDBY_PORT:-55492}
WORK=$(mktemp -d "${TMPDIR:-/tmp}/event-drill.XXXXXX")

cleanup() {
	"$PG_BIN/pg_ctl" -D "$WORK/primary" -m immediate stop >/dev/null 2>&1 || true
	"$PG_BIN/pg_ctl" -D "$WORK/standby" -m fast stop >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT

start() { # data dir, port
	"$PG_BIN/pg_ctl" -D "$1" -l "$1.log" -w -o "-p $2 -k $WORK -c listen_addresses= -c max_connections=200 -c wal_level=replica -c max_wal_senders=4" start >/dev/null
}

"$PG_BIN/initdb" -D "$WORK/primary" -U faas --auth=trust >/dev/null
start "$WORK/primary" "$PRIMARY_PORT"
"$PG_BIN/createdb" -h "$WORK" -p "$PRIMARY_PORT" -U faas drill
"$PG_BIN/pg_basebackup" -h "$WORK" -p "$PRIMARY_PORT" -U faas -D "$WORK/standby" -R -X stream
start "$WORK/standby" "$STANDBY_PORT"

FAAS_RUN_EVENT_FAILOVER_DRILL=1 \
FAAS_EVENT_DRILL_PRIMARY_URL="postgres://faas@/drill?host=$WORK&port=$PRIMARY_PORT&sslmode=disable" \
FAAS_EVENT_DRILL_STANDBY_URL="postgres://faas@/drill?host=$WORK&port=$STANDBY_PORT&sslmode=disable" \
FAAS_EVENT_DRILL_CRASH_CMD="'$PG_BIN/pg_ctl' -D '$WORK/primary' -m immediate stop" \
FAAS_EVENT_DRILL_PROMOTE_CMD="'$PG_BIN/pg_ctl' -D '$WORK/standby' -w promote" \
	"$GO" test -count=1 -v -timeout 30m -run '^TestEventFailoverDrill$' ./pkg/sched
