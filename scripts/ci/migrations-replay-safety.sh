#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${FAAS_REPLAY_CHECK_VERSIONS:-}" ]]; then
	go test -race -count=1 -v -run TestNewMigrationsAreReplaySafe ./migrations/...
	exit
fi

: "${RUNNER_TEMP:?RUNNER_TEMP is required}"
: "${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"
: "${GITHUB_RUN_ATTEMPT:?GITHUB_RUN_ATTEMPT is required}"

socket_dir="$RUNNER_TEMP/faas-replay-pg-socket"
bin_dir="$RUNNER_TEMP/faas-replay-pg-bin"
container="faas-replay-postgres-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
mkdir -p "$socket_dir" "$bin_dir"
chmod 0777 "$socket_dir"

docker run --detach --name "$container" --network none \
	--mount "type=bind,source=$socket_dir,target=/var/run/postgresql" \
	--env POSTGRES_USER=faas \
	--env POSTGRES_PASSWORD=faas \
	--env POSTGRES_DB=faas \
	--env 'POSTGRES_INITDB_ARGS=-c max_locks_per_transaction=512' \
	mirror.gcr.io/library/postgres:16 \
	postgres -c max_locks_per_transaction=512
cleanup() { docker rm --force "$container" >/dev/null 2>&1 || true; }
trap cleanup EXIT

init_complete=false
for attempt in $(seq 1 60); do
	if docker logs "$container" 2>&1 | grep -Fq 'PostgreSQL init process complete; ready for start up.'; then
		init_complete=true
		break
	fi
	sleep 1
done
if [[ "$init_complete" != true ]]; then
	docker exec "$container" ls -la /var/run/postgresql || true
	docker logs "$container"
	echo "::error::local PostgreSQL 16 initialization did not complete"
	exit 1
fi

ready=false
for attempt in $(seq 1 60); do
	if docker exec "$container" psql -h /var/run/postgresql -U faas -d faas -Atqc 'SELECT 1' >/dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 1
done
if [[ "$ready" != true ]]; then
	docker exec "$container" pg_isready -h /var/run/postgresql -U faas -d faas || true
	docker exec "$container" ls -la /var/run/postgresql || true
	docker logs "$container"
	echo "::error::local PostgreSQL 16 recovery socket did not accept SQL"
	exit 1
fi

# Recovery deliberately refuses TCP. Run the test's pg_dump with the matching
# PostgreSQL 16 binary inside the isolated, socket-only database container.
cat > "$bin_dir/pg_dump" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
exec docker exec \
	--env PGHOST=/var/run/postgresql \
	--env "PGPORT=${PGPORT:?}" \
	--env "PGDATABASE=${PGDATABASE:?}" \
	--env "PGUSER=${PGUSER:?}" \
	--env "PGPASSWORD=${PGPASSWORD-}" \
	--env "PGTZ=${PGTZ:?}" \
	"${FAAS_REPLAY_PG_CONTAINER:?}" pg_dump "$@"
SCRIPT
chmod 0755 "$bin_dir/pg_dump"

export DATABASE_URL="postgres:///faas?host=$socket_dir&user=faas"
export FAAS_REPLAY_PG_CONTAINER="$container"
export PATH="$bin_dir:$PATH"

go test -race -count=1 -v -run '^(TestApplicationStandardLedgerRecovery|TestMigrationRecovery)' ./pkg/db
go test -race -count=1 -v -run '^TestNewMigrationsAreReplaySafe$' ./migrations/...
