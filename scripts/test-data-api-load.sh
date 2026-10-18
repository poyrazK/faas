#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
: "${DATA_API_TEST_DATABASE_URL:?An administrative loopback PostgreSQL 16 fixture URL is required}"
: "${DATA_API_POSTGREST_BIN:?The checksum-pinned PostgREST executable is required}"
: "${DATA_API_LOAD_REPORT:?An explicit load report output path is required}"
# Install outside go:embed; never ship node_modules in a generated runtime.
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-data-api-load.XXXXXX")"
trap 'rm -rf "$runtime_dir"' EXIT
cp cmd/gregale/templates/data-api/*.mjs cmd/gregale/templates/data-api/package{,-lock}.json cmd/gregale/templates/data-api/Dockerfile "$runtime_dir/"
npm ci --prefix "$runtime_dir" --ignore-scripts
npm ci --prefix sdk/data --ignore-scripts
npm run build --prefix sdk/data
DATA_API_RUNTIME_DIR="$runtime_dir" node tests/data-api/load/verify.mjs --output "$DATA_API_LOAD_REPORT" "$@"
