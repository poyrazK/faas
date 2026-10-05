#!/usr/bin/env bash
# adr: 595. Run commands against a private PostgreSQL 16 Unix-socket cluster.
set -euo pipefail

pg_bin="${FAAS_REPLAY_PG_BIN:-/usr/lib/postgresql/16/bin}"
if [[ "$("$pg_bin/postgres" --version)" != *" 16."* ]]; then
  echo "migration recovery gate requires PostgreSQL 16" >&2
  exit 1
fi
replay_root="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/faas-replay.XXXXXX")"
cleanup() {
  "$pg_bin/pg_ctl" -D "$replay_root/data" stop -m immediate >/dev/null 2>&1 || true
  rm -rf "$replay_root"
}
trap cleanup EXIT
"$pg_bin/initdb" -D "$replay_root/data" -U faas --auth=trust --encoding=UTF8 --locale=C >/dev/null
"$pg_bin/pg_ctl" -D "$replay_root/data" -l "$replay_root/postgres.log" \
  -o "-k $replay_root -p 55432 -h '' -c timezone=UTC -c max_locks_per_transaction=512" -w start >/dev/null
"$pg_bin/createdb" -h "$replay_root" -p 55432 -U faas faas
export PATH="$pg_bin:$PATH"
export DATABASE_URL="postgresql://faas@/faas?host=$replay_root&port=55432"
export FAAS_PGTEST_TEMPLATE_DATABASE=1
unset FAAS_SKIP_PG_TESTS
"$@"
