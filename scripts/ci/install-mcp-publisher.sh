#!/usr/bin/env bash
# Downloads mcp-publisher $MCP_PUBLISHER_VERSION for linux/amd64 into the
# current directory and verifies its Sigstore bundle against the MCP
# registry's release workflow before unpacking it. Needs cosign on PATH.
set -euo pipefail

: "${MCP_PUBLISHER_VERSION:?set MCP_PUBLISHER_VERSION, e.g. v1.8.9}"

archive="mcp-publisher_linux_amd64.tar.gz"
base_url="https://github.com/modelcontextprotocol/registry/releases/download/${MCP_PUBLISHER_VERSION}"

curl --fail --location --silent --show-error --output "$archive" "$base_url/$archive"
curl --fail --location --silent --show-error --output "$archive.sigstore.json" "$base_url/$archive.sigstore.json"
cosign verify-blob \
  --bundle "$archive.sigstore.json" \
  --certificate-identity "https://github.com/modelcontextprotocol/registry/.github/workflows/release.yml@refs/tags/${MCP_PUBLISHER_VERSION}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "$archive"
tar xzf "$archive" mcp-publisher
rm -f "$archive" "$archive.sigstore.json"
