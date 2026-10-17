#!/usr/bin/env bash
# Exact CLI artifact, standalone dependencies, signed HTTP/HTTPS and process tests.
set -euo pipefail
command -v openssl >/dev/null || { echo 'OpenSSL is required for storage HTTPS acceptance.' >&2; exit 1; }
storage_starter_dir=$(mktemp -d)
trap 'rm -rf "$storage_starter_dir"' EXIT
python3 -m venv "$storage_starter_dir/venv"
storage_starter_python="$storage_starter_dir/venv/bin/python"
"$storage_starter_python" -m pip install --disable-pip-version-check -r cmd/gregale/templates/object-storage-python/requirements.txt
"$storage_starter_python" -m pip check
"$storage_starter_python" sdk/python/scripts/sync_binding_starters.py --check
GREGALE_PYTHON="$storage_starter_python" go test -race -p 2 ./cmd/gregale/templates -run '^TestPythonStorageStarter' -count=1 -v -timeout=180s
