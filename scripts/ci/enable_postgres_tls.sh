#!/usr/bin/env bash
# Enable native PostgreSQL TLS in a disposable GitHub Actions service container.
# Credentials still authenticate with the service image's SCRAM host rules.
set -euo pipefail
container_id=${1:?usage: enable_postgres_tls.sh SERVICE_CONTAINER_ID}
tls_dir=$(mktemp -d)
trap 'rm -rf "$tls_dir"' EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
  -keyout "$tls_dir/server.key" -out "$tls_dir/server.crt" 2>/dev/null
docker cp "$tls_dir/server.key" "$container_id:/tmp/qualification-server.key"
docker cp "$tls_dir/server.crt" "$container_id:/tmp/qualification-server.crt"
docker exec "$container_id" sh -eu -c '
  mv /tmp/qualification-server.key "$PGDATA/qualification-server.key"
  mv /tmp/qualification-server.crt "$PGDATA/qualification-server.crt"
  chown postgres:postgres "$PGDATA/qualification-server.key" "$PGDATA/qualification-server.crt"
  chmod 0600 "$PGDATA/qualification-server.key"
'
docker exec --user postgres "$container_id" psql -U faas -d faas -v ON_ERROR_STOP=1 \
  -c "ALTER SYSTEM SET ssl_cert_file = 'qualification-server.crt'" \
  -c "ALTER SYSTEM SET ssl_key_file = 'qualification-server.key'" \
  -c "ALTER SYSTEM SET ssl = 'on'" \
  -c 'SELECT pg_reload_conf()'
for attempt in 1 2 3 4 5; do
  if [[ $(docker exec --user postgres "$container_id" psql -U faas -d faas -At -c 'SHOW ssl') == on ]]; then
    echo "PostgreSQL TLS ready on attempt $attempt"
    exit 0
  fi
  sleep 1
done
echo 'PostgreSQL TLS did not become ready' >&2
exit 1
