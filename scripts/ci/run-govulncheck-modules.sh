#!/usr/bin/env bash
# Run govulncheck over every Go module tracked in the current repository
# (ADR-131). `./...` stops at a go.mod boundary, so the SDKs, the Terraform
# provider and the SDK test harnesses each need their own run. Every object a
# nested module's scan emits carries `module_dir`, which lets the normalizer
# name the go.mod to fix and check that each scan produced its config.

set -euo pipefail

output=${1:?output JSON path is required}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

tmp_dir=$(mktemp -d)
trap 'rm -rf "${tmp_dir}"' EXIT

"${script_dir}/run-govulncheck.sh" "${tmp_dir}/all.json" ./...

gomods=$(git ls-files -- '*/go.mod')
while IFS= read -r gomod; do
  [[ -n "${gomod}" ]] || continue
  # sdk/install-tests holds consumer templates: scripts/sdk_install_check.py
  # copies them into a scratch directory and points them at the built SDK
  # with a replace directive, so in the repository they cannot resolve.
  case "/${gomod}" in
    */testdata/*|*/vendor/*|*/install-tests/*) continue ;;
  esac
  dir=$(dirname "${gomod}")
  (cd "${dir}" && "${script_dir}/run-govulncheck.sh" "${tmp_dir}/module.json" ./...)
  jq -c --arg dir "${dir}" '. + {module_dir: $dir}' "${tmp_dir}/module.json" >> "${tmp_dir}/all.json"
done <<< "${gomods}"

cp "${tmp_dir}/all.json" "${output}"
