#!/usr/bin/env bash
# Source-derived requirements for the opt-in strict compute qualification lane.
# Sourced after native-e2e-verdict.sh by the dedicated native host runner.

financial_compute_required_tests() {
  local repo_root="$1" lane="$2" file names
  local files=() pattern
  case "${lane}" in
    postgres)
      files=("${repo_root}"/pkg/state/financialtest/financial_compute*_test.go)
      pattern='^func TestFinancial(Compute|Egress)[A-Za-z0-9_]+\('
      ;;
    native)
      files=("${repo_root}"/pkg/fcvm/financial_compute*metal_test.go)
      pattern='^func TestMetalFinancial[A-Za-z0-9_]+\('
      ;;
    *) echo "financial compute: unknown lane ${lane}" >&2; return 1 ;;
  esac
  names="$(
    for file in "${files[@]}"; do
      [[ -r "${file}" ]] || continue
      grep -hE "${pattern}" "${file}" || true
    done | sed -E 's/^func //; s/\(.*$//' | sort -u
  )" || return 1
  [[ -n "${names}" ]] || {
    echo "financial compute: ${lane} selects no tests" >&2; return 1;
  }
  printf '%s\n' "${names}"
}

financial_compute_verdict() {
  local repo_root="$1" lane="$2" log="$3" names required
  local tests=()
  names="$(financial_compute_required_tests "${repo_root}" "${lane}")" || return 1
  while IFS= read -r required; do tests+=("${required}"); done <<<"${names}"
  native_e2e_lane_verdict "${log}" "financial-compute-${lane}" "${tests[@]}"
}
