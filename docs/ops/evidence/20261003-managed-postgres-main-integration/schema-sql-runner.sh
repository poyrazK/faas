#!/usr/bin/env bash
set -Eeuo pipefail
root=/var/tmp/faas-metal-smoke-managed-pg-mega-pr-2cd325e9
user=kucukarslanhuseyinpoyraz_gmail_c
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TMPDIR=/run/mppr-2cd325e9-final GOCACHE="$root/go-build" GOMODCACHE="$root/go-mod" GOPATH="$root/go-path"
export GOTOOLCHAIN=local GOFLAGS=-p=1 GOMAXPROCS=1 GOGC=10 GOMEMLIMIT=384MiB
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0="$root/source"
mkdir -p "$TMPDIR"
chmod 1777 "$TMPDIR"
cd "$root/source"
sha256sum -c "$root/pinned-source-hashes.sha256" > "$root/head-source-match-before.log"
runuser -u "$user" -- /usr/lib/postgresql/16/bin/pg_ctl -D "$root/pg-data" -l "$root/postgres-final.log" -o "-h 127.0.0.1 -p 55437 -k $root/pg-socket -c max_connections=80 -c shared_buffers=64MB" -w start
trap 'runuser -u "$user" -- /usr/lib/postgresql/16/bin/pg_ctl -D "$root/pg-data" -m fast -w stop' EXIT
createdb -h 127.0.0.1 -p 55437 -U postgres gregale_guarded_fresh
export DATABASE_URL='postgres://postgres@127.0.0.1:55437/gregale_guarded_fresh?sslmode=disable'
runuser -u "$user" --preserve-environment -- /usr/local/bin/go run ./cmd/schema-dump -o "$root/guarded-fresh-schema.sql" > "$root/guarded-fresh-schema.log" 2>&1
# schema.sql omits pg_dump's extra trailing blank line.
python3 -c 'from pathlib import Path; p=Path("schema.sql"); q=Path("/var/tmp/faas-metal-smoke-managed-pg-mega-pr-2cd325e9/guarded-fresh-schema.sql"); assert p.read_bytes().rstrip()==q.read_bytes().rstrip()' > "$root/guarded-schema-match.log" 2>&1
printf 'fresh guarded migrations match canonical schema\n'
export DATABASE_URL='postgres://postgres@127.0.0.1:55437/gregale_tests?sslmode=disable'
export FAAS_PGTEST_TEMPLATE_DATABASE=1
runuser -u "$user" --preserve-environment -- /usr/local/bin/go test -race -v -count=1 -timeout=10m -run '^TestSQLLiteralsPrepareAgainstMigratedSchema$' ./pkg/state > "$root/final-sql-prepare.log" 2>&1
printf '0\n' > "$root/final-sql-prepare.exit"
runuser -u "$user" --preserve-environment -- /usr/local/bin/go test -race -v -count=1 -timeout=20m -run '^TestE2E_ManagedPostgres_' ./cmd/e2e > "$root/final-postgres-e2e.log" 2>&1
printf '0\n' > "$root/final-postgres-e2e.exit"
sha256sum -c "$root/pinned-source-hashes.sha256" > "$root/head-source-match-after.log"
printf 'managed PostgreSQL daemon E2E passed on d4155bdc5a1c8f5f3e5c2e68e2447ce4a1c34e38\n'
