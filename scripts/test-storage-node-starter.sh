#!/usr/bin/env bash
set -euo pipefail

# Install outside the embedded source tree: go:embed would otherwise include
# node_modules. The only removed directory is the one this harness creates.
starter_root="$(mktemp -d "${TMPDIR:-/tmp}/gregale-storage-node.XXXXXX")"
trap 'rm -rf -- "$starter_root"' EXIT
starter_fixture="$starter_root/object-storage-node"
mkdir "$starter_fixture"
export GREGALE_STORAGE_NODE_STARTER_FIXTURE="$starter_fixture"
go test -count=1 -run '^TestObjectStorageNode(MaterializeForQualification|RestoreManifest)$' ./cmd/gregale/templates
(cd "$starter_fixture" && npm ci --ignore-scripts --no-audit --no-fund && npm test)
go test -race -count=1 -timeout=120s -run '^TestManagedStorageNodeStarterJourney$' ./pkg/s3gateway
