#!/usr/bin/env bash
# Stage only the fixture and SDK sources; Gregale performs the Docker build
# inside its builder VM when this archive is deployed as a project.
set -Eeuo pipefail
[[ $# == 1 ]] || { echo 'usage: package-dev-bridge-fixture.sh OUTPUT.tar.gz' >&2; exit 1; }
output="$1"
[[ "$output" = /* ]] || output="$PWD/$output"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_stage="$(mktemp -d "${TMPDIR:-/tmp}/gregale-bridge-fixture.XXXXXXXX")"
trap 'rm -rf -- "$fixture_stage"' EXIT
mkdir -p "$fixture_stage/sdk/node" "$fixture_stage/tests/dev-bridge-acceptance/app"
cp "$repo_root/sdk/node/package.json" "$repo_root/sdk/node/package-lock.json" "$repo_root/sdk/node/tsconfig.json" "$fixture_stage/sdk/node/"
cp -R "$repo_root/sdk/node/src" "$fixture_stage/sdk/node/src"
cp "$repo_root/tests/dev-bridge-acceptance/Dockerfile" "$fixture_stage/tests/dev-bridge-acceptance/"
cp "$repo_root/tests/dev-bridge-acceptance/app/server.mjs" "$fixture_stage/tests/dev-bridge-acceptance/app/"
sed 's|context: ../..|context: .|' "$repo_root/tests/dev-bridge-acceptance/compose.yaml" > "$fixture_stage/compose.yaml"
tar -czf "$output" -C "$fixture_stage" .
echo "Fixture archive: $output"
