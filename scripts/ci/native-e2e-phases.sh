#!/usr/bin/env bash
# native-e2e-phases.sh — partition the metal e2e suite into named phases.
#
# The gate used to run all 50 metal-tagged tests as a single `go test`
# invocation: one step, one 12,000-line log, 45 minutes, and a single
# pass/fail. When it broke you learned that "the gate is red", not which layer
# broke, and one fix per run meant one layer per 45 minutes. Eight consecutive
# runs were spent that way.
#
# Phases give each layer its own CI step with its own name, duration and
# verdict, so a run reports the whole picture at once.
#
# PARTITION BY FILE, NEVER BY TEST NAME. The run set has always been derived
# from source (every top-level Test func in a //go:build metal file) precisely
# so the gate cannot be quietly narrowed to a hand-picked list — that is how
# the retired self-hosted metal job shrank to one test and looked green for 100
# dispatches. Phases keep that property: a phase names FILES, the test names
# come from the same derivation as before, and
# native_e2e_assert_phase_partition below fails if any metal test is in no
# phase or in more than one. Adding a metal test file without assigning it is
# therefore a hard error, not a silent hole.

set -Eeuo pipefail

# NATIVE_E2E_PHASES is the ordered list of phase names. Order is the execution
# order: cheap structural checks first, so an obviously broken tree fails in
# seconds rather than after the first 20-minute build.
NATIVE_E2E_PHASES=(
  fixtures
  build
  deploy
  wake
  streaming
  logs
  security
  twonode
  jobs
)

# native_e2e_phase_files echoes the test files belonging to a phase, one per
# line. Globs are matched against cmd/e2e; a glob that matches nothing is a
# hard error via the partition assert, not a silent empty phase.
native_e2e_phase_files() {
  case "${1:?phase name required}" in
    # Shape/contract checks over the fixture apps. No microVM, seconds to run —
    # first so a broken fixture tree is reported before anything boots.
    fixtures) printf '%s\n' fixtures_meta_test.go ;;
    # Source -> builderd -> builder microVM -> OCI image. The most expensive
    # phase by far (a stalled build rides out its 10-minute budget), and the
    # one everything below depends on.
    build) printf '%s\n' build_metal_test.go catalog_runtime_metal_test.go ;;
    # Deploy paths: image, source tarball, healthcheck, port override, secrets.
    deploy) printf '%s\n' \
      deploy_healthcheck_metal_test.go \
      deploy_override_port_metal_test.go \
      deploy_wake_metal_test.go \
      source_deploy_wake_metal_test.go \
      secrets_image_deploy_e2e_test.go ;;
    # Wake scheduling: timeline emission, cross-schedd dedup, CPU fairness.
    wake) printf '%s\n' \
      wake_timeline_metal_test.go \
      fleet_wake_dedup_e2e_test.go \
      cpu_fairness_test.go ;;
    # Response streaming and the h2c/gRPC inner leg (G19.3).
    streaming) printf '%s\n' streaming_metal_test.go bridge_h2c_terminator_metal_test.go ;;
    # App log delivery (SSE).
    logs) printf '%s\n' logs_e2e_test.go ;;
    # Spec §11 ship-blocking fences. Small, and must never be skipped quietly.
    security) printf '%s\n' sec11_memory_max_e2e_test.go sec11_seccomp_e2e_test.go ;;
    # Multi-node control plane: recovery arbiter, drain, heartbeat drills.
    twonode) printf '%s\n' twonode_failure_safe_metal_test.go twonode_runbook_test.go ;;
    # Unimplemented stubs (#2569). Non-blocking in the workflow until they are
    # implemented, so 11 known failures cannot bury a real regression
    # elsewhere. Kept as a visible phase rather than deleted so the debt stays
    # in view.
    jobs) printf '%s\n' jobs_metal_test.go ;;
    *) echo "native-e2e-phases: unknown phase: $1" >&2; return 1 ;;
  esac
}

# native_e2e_phase_tests echoes the top-level Test funcs in a phase's files.
# Same extraction the whole-suite derivation uses: `^func Test...` in a
# metal-tagged file. The `|| true` is load-bearing — a listed file with no
# top-level Test func (fixtures_test.go is one) makes grep exit 1, and under
# `set -o pipefail` that kills the caller.
native_e2e_phase_tests() {
  local phase="${1:?phase name required}" root="${2:-.}" file path
  while IFS= read -r file; do
    path="${root}/cmd/e2e/${file}"
    [[ -f "${path}" ]] || continue
    grep -hoE '^func Test[A-Za-z0-9_]+\(' "${path}" 2>/dev/null || true
  done < <(native_e2e_phase_files "${phase}") |
    sed -E 's/^func //; s/\($//' | sort -u
}

# native_e2e_phase_regex builds the -run anchor for a phase. Empty phases are
# refused: a phase that selects nothing would report "ok" having executed
# nothing, which is the exact failure the whole-suite derivation exists to
# prevent.
native_e2e_phase_regex() {
  local phase="${1:?phase name required}" root="${2:-.}" tests
  tests="$(native_e2e_phase_tests "${phase}" "${root}")"
  [[ -n "${tests}" ]] || {
    echo "native-e2e-phases: phase ${phase} selects no tests" >&2
    return 1
  }
  printf '^(%s)$\n' "$(printf '%s\n' "${tests}" | paste -sd'|' -)"
}

# native_e2e_assert_phase_partition fails when the phases are not an exact
# partition of the metal suite. Two ways to break it, both silent otherwise:
# a new metal test file nobody assigned (the test never runs, and the gate
# looks unchanged), or a file listed in two phases (the test runs twice and a
# flake reads as two failures).
native_e2e_assert_phase_partition() {
  local root="${1:-.}" phase all_tests phase_tests seen dupes missing
  all_tests="$(native_e2e_metal_tests "${root}")"

  seen=""
  dupes=""
  for phase in "${NATIVE_E2E_PHASES[@]}"; do
    phase_tests="$(native_e2e_phase_tests "${phase}" "${root}")"
    while IFS= read -r t; do
      [[ -n "${t}" ]] || continue
      if printf '%s\n' "${seen}" | grep -Fxq -- "${t}"; then
        dupes+="${t}"$'\n'
      fi
      seen+="${t}"$'\n'
    done <<<"${phase_tests}"
  done

  missing="$(comm -23 \
    <(printf '%s\n' "${all_tests}" | sort -u) \
    <(printf '%s\n' "${seen}" | grep -v '^$' | sort -u))"

  local rc=0
  if [[ -n "${missing}" ]]; then
    echo "native-e2e-phases: metal tests assigned to no phase:" >&2
    printf '  %s\n' ${missing} >&2
    echo "Add the test's file to a phase in native_e2e_phase_files." >&2
    rc=1
  fi
  if [[ -n "${dupes}" ]]; then
    echo "native-e2e-phases: metal tests assigned to more than one phase:" >&2
    printf '  %s\n' ${dupes} >&2
    rc=1
  fi
  return "${rc}"
}
