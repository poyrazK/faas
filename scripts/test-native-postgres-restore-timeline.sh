#!/bin/sh
# Exercise archive verification and physical replay after a standby promotion.
# Run with PostgreSQL 16's bin directory on PATH.
set -eu

repo=$(CDPATH='' cd "$(dirname "$0")/.." && pwd)
fixture=$(mktemp -d /tmp/gregale-timeline.XXXXXX)
primary="$fixture/primary"
standby="$fixture/standby"
replay="$fixture/replay"
mkdir -p "$primary" "$standby" "$replay" "$fixture/base" "$fixture/archive"
cleanup() {
    result=$?
    if [ "$result" -ne 0 ]; then
        for instance in "$primary" "$standby" "$replay"; do
            if [ -f "$instance/postgres.log" ]; then tail -25 "$instance/postgres.log"; fi
        done
    fi
    for instance in "$primary" "$standby" "$replay"; do
        pg_ctl -D "$instance/data" -m immediate stop >/dev/null 2>&1 || true
    done
    rm -rf "$fixture"
}
trap cleanup EXIT

query() {
    instance=$1
    shift
    psql -X -qtA -v ON_ERROR_STOP=1 -h "$instance" -d postgres "$@"
}
instant() {
    query "$1" -c "SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"')"
}
start() {
    pg_ctl -D "$1/data" -l "$1/postgres.log" -o "-h '' -k $1" -w start >/dev/null
}

initdb -D "$primary/data" --auth=trust --no-locale --encoding=UTF8 >/dev/null
cat >> "$primary/data/postgresql.conf" <<CONF
archive_mode = on
archive_command = 'test -f "$fixture/archive/%f" || (cp "%p" "$fixture/archive/%f.tmp" && mv "$fixture/archive/%f.tmp" "$fixture/archive/%f")'
CONF
start "$primary"
query "$primary" -c 'CREATE TABLE restore_evidence (id int PRIMARY KEY); INSERT INTO restore_evidence VALUES (1)'
pg_basebackup -h "$primary" -D "$fixture/base" -Ft -X stream -c fast -w >/dev/null
pg_basebackup -h "$primary" -D "$standby/data" -Fp -X stream -R -c fast -w >/dev/null
start "$standby"
query "$primary" -c 'SELECT pg_switch_wal()' >/dev/null
query "$primary" -c 'INSERT INTO restore_evidence VALUES (2)'
instant "$primary" > "$fixture/before.txt"
query "$primary" -c 'INSERT INTO restore_evidence VALUES (3)'
lsn=$(query "$primary" -c 'SELECT pg_current_wal_flush_lsn()')
attempt=0
until [ "$(query "$standby" -c "SELECT pg_last_wal_replay_lsn() >= '$lsn'::pg_lsn")" = t ]; do
    attempt=$((attempt + 1))
    test "$attempt" -lt 30
    sleep 1
done
pg_ctl -D "$primary/data" -m immediate -w stop >/dev/null
pg_ctl -D "$standby/data" -w promote >/dev/null
query "$standby" -c 'INSERT INTO restore_evidence VALUES (4)'
instant "$standby" > "$fixture/after.txt"
query "$standby" -c 'INSERT INTO restore_evidence VALUES (5)'
segment=$(query "$standby" -c 'SELECT pg_walfile_name(pg_current_wal_insert_lsn() + 1)')
query "$standby" -c 'SELECT pg_switch_wal()' >/dev/null
attempt=0
until [ -f "$fixture/archive/00000002.history" ] && [ -f "$fixture/archive/$segment" ]; do
    attempt=$((attempt + 1))
    test "$attempt" -lt 30
    sleep 1
done
printf '%s\n' "$segment" > "$fixture/child-segment.txt"
pg_ctl -D "$standby/data" -m immediate -w stop >/dev/null

(cd "$repo" && FAAS_RESTORE_TIMELINE_FIXTURE="$fixture" go test ./pkg/dbarchive -count=1 -run '^TestRestoreCheckWithPromotedPostgres$')

for choice in before after; do
    mkdir -p "$replay/data"
    tar -xf "$fixture/base/base.tar" -C "$replay/data"
    chmod 0700 "$replay/data"
    mkdir -p "$replay/data/pg_wal"
    tar -xf "$fixture/base/pg_wal.tar" -C "$replay/data/pg_wal"
    touch "$replay/data/recovery.signal"
    target=$(sed -e 's/T/ /' -e 's/Z$/+00/' "$fixture/$choice.txt")
    cat >> "$replay/data/postgresql.conf" <<CONF
archive_mode = off
restore_command = 'cp "$fixture/archive/%f" "%p"'
recovery_target_time = '$target'
recovery_target_timeline = '2'
recovery_target_action = promote
CONF
    start "$replay"
    attempt=0
    while :; do
        ids=$(query "$replay" -c "SELECT string_agg(id::text,',' ORDER BY id) FROM restore_evidence HAVING NOT pg_is_in_recovery()")
        if [ -n "$ids" ]; then break; fi
        attempt=$((attempt + 1))
        test "$attempt" -lt 30
        sleep 1
    done
    expected='1,2'
    if [ "$choice" = after ]; then expected='1,2,3,4'; fi
    test "$ids" = "$expected" || { printf '%s promotion recovered rows %s, expected %s\n' "$choice" "$ids" "$expected"; exit 1; }
    printf '%s promotion, pinned timeline 2 recovered rows %s\n' "$choice" "$ids"
    pg_ctl -D "$replay/data" -m immediate -w stop >/dev/null
    rm -rf "$replay/data"
done
