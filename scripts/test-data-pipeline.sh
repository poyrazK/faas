#!/usr/bin/env bash
# Real native replication acceptance on a disposable, digest-pinned PostgreSQL.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ ! -d cmd/gregale/templates/data-api/node_modules/jose ]; then
  echo 'Install the pinned runtime test dependencies: npm ci --prefix cmd/gregale/templates/data-api' >&2
  exit 1
fi
image=postgres@sha256:1a6ab3f5345eb6dbe04a1349529caabdb0ab09293a09590fad07b2246bfa4b54
fixture_dir=$(mktemp -d)
fixture_container=''
engine_container=''
cleanup() {
  if [ -n "$engine_container" ]; then docker rm -f "$engine_container" >/dev/null; fi
  if [ -n "$fixture_container" ]; then docker rm -fv "$fixture_container" >/dev/null; fi
  rm -rf "$fixture_dir"
}
trap cleanup EXIT
chmod 755 "$fixture_dir"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj '/CN=localhost' \
 -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
 -keyout "$fixture_dir/server.key" -out "$fixture_dir/server.crt" >/dev/null 2>&1
chmod 644 "$fixture_dir/server.crt"
engine_container=$(docker create --label gregale.pipeline.tests=true postgrest/postgrest@sha256:7c5840727ad683b0fdbfb9810930665b34f8b44884a4dc740aa8844748a2b85e)
docker cp "$engine_container:/bin/postgrest" "$fixture_dir/postgrest" >/dev/null
docker rm "$engine_container" >/dev/null
engine_container=''
export POSTGREST_BINARY="$fixture_dir/postgrest"
fixture_container=$(docker run --rm -d --label gregale.pipeline.tests=true \
 -p 127.0.0.1::5432 -v "$fixture_dir:/pipeline-cert:ro" \
 -e POSTGRES_USER=faas -e POSTGRES_DB=faas -e POSTGRES_HOST_AUTH_METHOD=trust \
 "$image" sh -ec 'cp /pipeline-cert/server.key /var/lib/postgresql/server.key; chown postgres:postgres /var/lib/postgresql/server.key; chmod 600 /var/lib/postgresql/server.key; exec docker-entrypoint.sh postgres -c wal_level=logical -c max_slot_wal_keep_size=1GB -c max_replication_slots=20 -c max_wal_senders=20 -c max_logical_replication_workers=10 -c max_worker_processes=16 -c wal_receiver_status_interval=1s -c ssl=on -c ssl_cert_file=/pipeline-cert/server.crt -c ssl_key_file=/var/lib/postgresql/server.key')
fixture_port=$(docker port "$fixture_container" 5432/tcp | sed -n 's/^127\.0\.0\.1://p')
test -n "$fixture_port"
ready=false
for attempt in {1..40}; do
 if docker exec "$fixture_container" pg_isready -U faas -d faas >/dev/null 2>&1; then ready=true; break; fi
 sleep 0.25
done
if [ "$ready" != true ]; then docker logs --tail 30 "$fixture_container" >&2; exit 1; fi
export DATABASE_URL="postgres://faas@127.0.0.1:$fixture_port/faas?sslmode=disable"
export DATA_PIPELINE_TEST_DSN="postgres://faas:test@localhost:$fixture_port/faas?sslmode=verify-full"
export DATA_PIPELINE_TEST_CA="$fixture_dir/server.crt"
export PGSSLROOTCERT="$fixture_dir/server.crt"
export DATA_PIPELINE_TEST_SUBSCRIBER_CA=/pipeline-cert/server.crt
export FAAS_PGTEST_TEMPLATE_DATABASE=0
unset FAAS_SKIP_PG_TESTS
go test -race -count=1 ./pkg/datapipeline ./pkg/apid/datapipelines ./pkg/apid/apidsource ./pkg/dashboard
go test -race -count=1 -run '^(TestCapacity|TestConnectionBudget|TestPipelineDatabase|TestPipelineAndWritable|TestSetup|TestCreateWithID)' ./pkg/managedpostgres
go test -race -count=1 -run '^TestBoundedCredential' ./pkg/managedpostgres/neon
# Match the platform CI's bounded CLI compiler settings as the command grows.
go test '-gcflags=github.com/onebox-faas/faas/cmd/gregale=-c=1 -dwarf=false' -race -count=1 -run '^(TestPipeline|TestConnectionCheck|TestDataAPIConnectionBudget|TestConnectionBudgetAccepts)' ./cmd/gregale
