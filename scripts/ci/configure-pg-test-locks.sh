#!/usr/bin/env bash
# adr: 590. Parallel private test databases share one PostgreSQL lock table.
set -euo pipefail

container_id="${1:?PostgreSQL service container ID is required}"
docker exec "$container_id" sh -c 'printf "%s\n" "max_locks_per_transaction=512" >> "$PGDATA/postgresql.conf"'
docker restart "$container_id" >/dev/null
for ((attempt = 0; attempt < 30; attempt++)); do
  if docker exec "$container_id" pg_isready -U faas -d faas >/dev/null 2>&1; then
    actual="$(docker exec "$container_id" psql -U faas -d faas -Atqc 'SHOW max_locks_per_transaction')"
    [[ "$actual" == 512 ]] || { echo "PostgreSQL test lock capacity was not applied" >&2; exit 1; }
    exit 0
  fi
  sleep 1
done
echo "PostgreSQL test service did not restart" >&2
exit 1
