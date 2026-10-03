#!/usr/bin/env bash
# Nonexecuting catalog gate used by the pinned reusable workflow.
set -euo pipefail
cli="${1:?provide the pinned Gregale CLI}"
baseline_root=$(realpath "$GITHUB_WORKSPACE/caller-baseline")
baseline="$baseline_root/$GREG_MCP_BASELINE"
resolved=$(realpath "$baseline")
if ! [[ "$resolved" == "$baseline_root/"* && -f "$baseline" && ! -L "$baseline" ]]; then
  echo "baseline must be a regular file inside its checkout" >&2
  exit 1
fi
receipt="$RUNNER_TEMP/mcp-catalog-receipt"
mkdir -p "$receipt"
cp "$baseline" "$receipt/baseline.json"
capture=(mcp lock --url "$GREG_MCP_ENDPOINT" --out "$receipt/candidate.json" --json)
if [[ -n "${GREG_MCP_ENDPOINT_TOKEN:-}" ]]; then capture+=(--token-env GREG_MCP_ENDPOINT_TOKEN); fi
if [[ "${GREG_MCP_LEGACY:-false}" == true ]]; then capture+=(--legacy); fi
"$cli" "${capture[@]}" > "$receipt/capture.json"
compare=(mcp diff --before "$baseline" --after "$receipt/candidate.json" --check --json)
if [[ "${GREG_MCP_STRICT:-true}" == true ]]; then compare+=(--strict-catalog); fi
"$cli" "${compare[@]}" > "$receipt/diff.json"
