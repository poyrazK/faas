#!/usr/bin/env bash
# Exact CLI-artifact acceptance against the job's disposable TLS/SCRAM service.
set -euo pipefail
starter_ca=${1:?usage: test_python_postgres_starter.sh TRUSTED_CA_PATH}
starter_dir=$(mktemp -d)
trap 'rm -rf "$starter_dir"' EXIT
python3 -m venv "$starter_dir/venv"
starter_python="$starter_dir/venv/bin/python"
"$starter_python" -m pip install --disable-pip-version-check -r cmd/gregale/templates/rest-api-postgres-python/requirements.txt
"$starter_python" sdk/python/scripts/sync_binding_starters.py --check
export GREGALE_PYTHON="$starter_python"
export GREGALE_PYTHON_POSTGRES_TEST=1
# Preserve the selected fixture identity, upgrade transport verification and
# add its explicit CA without displaying credentials in the job log.
DATABASE_URL=$("$starter_python" - "$starter_ca" <<'PY'
import os
import sys
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit
url = urlsplit(os.environ["DATABASE_URL"])
options = dict(parse_qsl(url.query))
options.update(sslmode="verify-full", sslrootcert=sys.argv[1])
print(urlunsplit(url._replace(query=urlencode(options))))
PY
)
export DATABASE_URL
go test -race -p 2 ./cmd/gregale/templates -run '^TestPythonPostgresStarter' -count=1 -v -timeout=180s
go test -race -p 1 ./pkg/e2etest/postgresstarter -run '^TestPythonStarterSystemTrust$' -count=1 -v -timeout=60s
(
  cd examples/object-storage-rotation-python
  "$starter_python" -m pip install --disable-pip-version-check -r requirements.txt pytest
)
GREGALE_ASYNC_STORAGE_POSTGRES_TEST=1 PYTHONPATH=sdk/python "$starter_python" -m pytest -q \
  sdk/python/tests/test_async_object_storage_runtime.py \
  sdk/python/tests/test_async_storage_example.py \
  sdk/python/tests/test_async_storage_postgres_integration.py
