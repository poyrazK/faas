#!/usr/bin/env bash
# scripts/railpack-platform-layers.sh — print the platformLayers entries for
# pkg/builderd/platform_blobs.go (ADR-20261031103801).
#
# Usage: scripts/railpack-platform-layers.sh ghcr.io/railwayapp/railpack-builder:mise-2026.8.13
#
# Run it when RAILPACK_VERSION changes, with the builder image tag the new
# Railpack plans use (a build log's "slowest build steps" names it), and
# update platformLayersRailpackVersion with the list. Only the amd64 image's
# layers are listed: production builders are amd64.
set -euo pipefail

ref="${1:?usage: $0 ghcr.io/<repo>:<tag>}"
case "$ref" in
  ghcr.io/*:*) ;;
  *) echo "want ghcr.io/<repo>:<tag>, got $ref" >&2; exit 2 ;;
esac
repo="${ref#ghcr.io/}"; tag="${repo##*:}"; repo="${repo%:*}"
token="$(curl -fsS "https://ghcr.io/token?scope=repository:${repo}:pull" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')"
accept='application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json'
get() { curl -fsS -H "Authorization: Bearer ${token}" -H "Accept: ${accept}" "https://ghcr.io/v2/${repo}/manifests/$1"; }
amd64="$(get "$tag" | python3 -c 'import json,sys
m = json.load(sys.stdin)
print(next(x["digest"] for x in m["manifests"] if x.get("platform", {}).get("architecture") == "amd64" and x.get("platform", {}).get("os") == "linux"))')"
get "$amd64" | python3 -c 'import json,sys
for layer in json.load(sys.stdin)["layers"]:
    print("\t\"%s\": {}," % layer["digest"].removeprefix("sha256:"))'
