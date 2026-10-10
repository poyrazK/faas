#!/usr/bin/env bash
set -euo pipefail

: "${GREGALE_BIN:?set GREGALE_BIN to the built Gregale CLI}"
: "${SDK_PACK_DIR:?set SDK_PACK_DIR to the directory containing the packed Node SDK}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ ! -x "$GREGALE_BIN" ]]; then
  echo "Gregale CLI is not executable: $GREGALE_BIN" >&2
  exit 1
fi

sdk_archive="$(find "$SDK_PACK_DIR" -maxdepth 1 -type f -name 'gregale-sdk-node-*.tgz' -print -quit)"
if [[ -z "$sdk_archive" ]]; then
  echo "No packed gregale-sdk-node archive found in $SDK_PACK_DIR" >&2
  exit 1
fi

work_root="$(mktemp -d "${TMPDIR:-/tmp}/gregale-operation-starters.XXXXXX")"
trap 'rm -rf -- "$work_root"' EXIT

check_tree_matches() {
  local expected_root="$1"
  local actual_root="$2"
  local description="$3"
  local expected_file
  local relative_path
  local actual_file

  while IFS= read -r -d '' expected_file; do
    relative_path="${expected_file#"$expected_root"/}"
    actual_file="$actual_root/$relative_path"
    if [[ ! -f "$actual_file" ]] || ! cmp -s "$expected_file" "$actual_file"; then
      echo "$description differs at $relative_path" >&2
      return 1
    fi
  done < <(find "$expected_root" -type f -print0)
}

check_starter() {
  local template="$1"
  local app="$2"
  local plan="$3"
  local destination="$work_root/$template"
  local template_root="$repo_root/cmd/gregale/templates/$template"
  local example_root="$repo_root/examples/$template"

  echo "Checking $template"
  check_tree_matches "$template_root" "$example_root" "template/example"
  "$GREGALE_BIN" init --template "$template" --path "$destination" >/dev/null
  check_tree_matches "$template_root" "$destination" "template/init output"
  mkdir -p "$destination/packages"
  cp "$sdk_archive" "$destination/packages/gregale-sdk.tgz"

  "$GREGALE_BIN" customer-operations types --dir "$destination" --app "$app" --plan "$plan"
  "$GREGALE_BIN" customer-operations types --dir "$destination" --app "$app" --plan "$plan" --check

  (
    cd "$destination"
    npm install --ignore-scripts --no-audit --no-fund
    npm run typecheck
  )
}

check_starter customer-operation-export exports hobby
check_starter customer-operation-job-export exports pro
check_starter customer-operation-workflow-export exports hobby

echo "All customer Operations starters match their generated types and the packed SDK."
