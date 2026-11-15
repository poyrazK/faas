#!/usr/bin/env bash
set -euo pipefail
task_fixture="gregale-task-execution-bindings-$$"
task_created=false
task_ram=$(mktemp -d "${TMPDIR:-/tmp}/gregale-task-bindings.XXXXXX")
task_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
task_result="$task_ram/results.log"
cleanup() { if "$task_created"; then docker rm -f "$task_fixture" >/dev/null; fi; rm -rf "$task_ram"; }
trap cleanup EXIT
mkdir -p "$task_ram/worker"
docker run --detach --name "$task_fixture" --label gregale.task=task-execution-bindings-acceptance \
  --tmpfs /var/lib/postgresql/data:rw,size=512m --publish 127.0.0.1::5432 \
  --env POSTGRES_USER=faas --env POSTGRES_DB=faas --env POSTGRES_HOST_AUTH_METHOD=trust postgres:16 >/dev/null
task_created=true
task_address=$(docker port "$task_fixture" 5432/tcp)
for attempt in $(seq 1 40); do
  if docker exec "$task_fixture" pg_isready --username faas --dbname faas >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec -i "$task_fixture" psql -U faas -d faas -v ON_ERROR_STOP=1 >"$task_result" <<'SQL'
CREATE ROLE task_migrator LOGIN;
CREATE ROLE task_runtime LOGIN;
CREATE ROLE task_observer LOGIN;
CREATE SCHEMA task_exec AUTHORIZATION task_migrator;
REVOKE ALL ON SCHEMA task_exec FROM PUBLIC;
GRANT USAGE ON SCHEMA task_exec TO task_runtime,task_observer;
ALTER ROLE task_migrator SET search_path=task_exec;
ALTER ROLE task_runtime SET search_path=task_exec;
ALTER ROLE task_observer SET search_path=task_exec;
SET ROLE task_migrator;
CREATE TABLE task_exec.gregale_mcp_tasks(id int);
CREATE TABLE task_exec.gregale_mcp_task_owners(id int);
CREATE TABLE task_exec.gregale_mcp_task_fairness(id int);
CREATE TABLE task_exec.gregale_mcp_task_workers(id int);
CREATE TABLE task_exec.gregale_mcp_task_worker_handlers(id int);
CREATE TABLE task_exec.gregale_mcp_task_schema(id int);
CREATE TABLE task_exec.gregale_mcp_task_crypto_keys(id int);
CREATE TABLE task_exec.gregale_mcp_task_admission(id int);
CREATE TABLE task_exec.gregale_mcp_task_admission_namespaces(id int);
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA task_exec TO task_runtime;
GRANT SELECT ON ALL TABLES IN SCHEMA task_exec TO task_observer;
SQL
printf '%s\n' '{"type":"module","dependencies":{"pg":"8.16.3"}}' >"$task_ram/worker/package.json"
printf '%s\n' '{"tasks":{"database_url_env":"TASK_RUNTIME_URL"}}' >"$task_ram/worker/gregale-mcp.json"
npm install --prefix "$task_ram/worker" --cache "$task_ram/cache" --ignore-scripts --no-audit --no-fund >>"$task_result" 2>&1
cp "$task_repo/cmd/gregale/task_execution_bindings.mjs" "$task_ram/verifier.mjs"
task_verify_code=$(cat "$task_ram/verifier.mjs")
export TASK_RUNTIME_URL="postgres://task_runtime@$task_address/faas"
export MCP_TASK_MIGRATION_DATABASE_URL="postgres://task_migrator@$task_address/faas"
export MCP_TASK_OBSERVER_DATABASE_URL="postgres://task_observer@$task_address/faas"
cd "$task_ram/worker"
check() {
  local task_want="$1" task_case="$2" task_actual=0
  env -u FAAS_TOKEN -u GREGALE_AGENT_KEY node --input-type=module --eval "$task_verify_code" >"$task_ram/output" 2>"$task_ram/error" || task_actual=$?
  if [ "$task_actual" -ne "$task_want" ]; then printf 'FAIL: %s (%s)\n' "$task_case" "$task_actual"; exit 1; fi
  printf 'PASS: %s\n' "$task_case" | tee -a "$task_result"
}
check 0 'three separate least-privilege roles'
docker exec "$task_fixture" psql -U faas -d faas -c 'GRANT INSERT ON task_exec.gregale_mcp_tasks TO task_observer' >>"$task_result"
check 1 'observer write privilege rejected'
docker exec "$task_fixture" psql -U faas -d faas -c 'REVOKE INSERT ON task_exec.gregale_mcp_tasks FROM task_observer' >>"$task_result"
MCP_TASK_MIGRATION_DATABASE_URL="$TASK_RUNTIME_URL" check 1 'runtime/migration role alias rejected'
docker exec "$task_fixture" psql -U faas -d faas -c 'ALTER ROLE task_runtime SUPERUSER' >>"$task_result"
check 1 'runtime superuser rejected'
docker exec "$task_fixture" psql -U faas -d faas -c 'ALTER ROLE task_runtime NOSUPERUSER' >>"$task_result"
MCP_TASK_OBSERVER_DATABASE_URL="postgres://task_observer@$task_address/postgres" check 1 'different database rejected'
docker exec "$task_fixture" psql -U faas -d faas -c 'GRANT task_migrator TO task_observer' >>"$task_result"
check 1 'observer owner membership rejected'
docker exec "$task_fixture" psql -U faas -d faas -c 'REVOKE task_migrator FROM task_observer' >>"$task_result"
check 0 'valid roles recover after fixture-only reversals'
