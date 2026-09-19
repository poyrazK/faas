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
      direct_oci_autoscale_metal_test.go \
      direct_oci_fullrootfs_metal_test.go \
      direct_oci_port_metal_test.go \
      source_deploy_wake_metal_test.go \
      secrets_image_deploy_e2e_test.go \
      tcp_ingress_metal_test.go ;;
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

# ---------------------------------------------------------------------------
# Lanes. A phase partitions the FULL suite; a lane is a deliberately small,
# hand-picked subset for a different question. The gate's whole-suite claim
# ("every metal test ran") belongs to the phases and is unchanged. A lane
# makes no such claim and must never be mistaken for one — which is why it is
# selected by an explicit dispatch input, never by default on a schedule.
#
# smoke — "can a beta customer's app go live and answer?" One source deploy
# end to end (upload -> builderd -> builder microVM -> imaged -> live -> cold
# wake -> HTTP -> park), one prebuilt-image deploy + wake, the two §11
# ship-blocking fences, and the no-VM fixture checks. That single path touches
# apid, builderd, imaged, schedd, vmmd and gatewayd. ~10 minutes, versus ~60
# for the full matrix, which is what makes fix-then-rerun a loop rather than
# an afternoon. The full run had 16 real builds per pass; nine of them are
# variants of the same fixture, and none of that tells you sooner whether the
# beta path works.
NATIVE_E2E_LANES=(smoke)

# NATIVE_E2E_SMOKE_TESTS lists the smoke lane by NAME. Hand-picked on purpose
# (see above); native_e2e_assert_lanes below fails if any name is not a real
# metal test, so a rename or a lost build tag cannot quietly shrink the lane.
NATIVE_E2E_SMOKE_TESTS=(
  TestSourceDeployWakeMetal
  TestDeployWakeMetal
  TestSec11_MemoryMaxFenceEnforced_CrossProcess
  TestSec11_SeccompFilterEnforced_CrossProcess
)

# native_e2e_lane_tests echoes a lane's tests, one per line. smoke is the
# explicit list plus every fixtures-phase test (no microVM, seconds).
native_e2e_lane_tests() {
  local lane="${1:?lane name required}" root="${2:-.}"
  case "${lane}" in
    smoke)
      {
        native_e2e_phase_tests fixtures "${root}"
        printf '%s\n' "${NATIVE_E2E_SMOKE_TESTS[@]}"
      } | sort -u
      ;;
    *) echo "native-e2e-phases: unknown lane: ${lane}" >&2; return 1 ;;
  esac
}

# native_e2e_lane_regex builds the -run anchor for a lane. Refuses an empty
# lane for the same reason native_e2e_phase_regex does.
native_e2e_lane_regex() {
  local lane="${1:?lane name required}" root="${2:-.}" tests
  tests="$(native_e2e_lane_tests "${lane}" "${root}")"
  [[ -n "${tests}" ]] || {
    echo "native-e2e-phases: lane ${lane} selects no tests" >&2
    return 1
  }
  printf '^(%s)$\n' "$(printf '%s\n' "${tests}" | paste -sd'|' -)"
}

# native_e2e_is_lane reports whether a name is a lane rather than a phase.
native_e2e_is_lane() {
  local name="${1:?name required}" lane
  for lane in "${NATIVE_E2E_LANES[@]}"; do
    [[ "${name}" == "${lane}" ]] && return 0
  done
  return 1
}

# native_e2e_assert_lanes fails when a lane names a test that is not in the
# metal-tagged set — the way a lane silently shrinks.
native_e2e_assert_lanes() {
  local root="${1:-.}" all_tests lane t rc=0
  all_tests="$(native_e2e_metal_tests "${root}")"
  for lane in "${NATIVE_E2E_LANES[@]}"; do
    while IFS= read -r t; do
      [[ -n "${t}" ]] || continue
      printf '%s\n' "${all_tests}" | grep -qx -- "${t}" || {
        echo "native-e2e-phases: lane ${lane} names ${t}, which is not a metal-tagged test (renamed, or lost its build tag)" >&2
        rc=1
      }
    done < <(native_e2e_lane_tests "${lane}" "${root}")
  done
  return "${rc}"
}

# native_e2e_is_selector reports whether a name is a phase or a lane — the
# set FAAS_E2E_PHASE may carry. The runner validates against THIS, not
# NATIVE_E2E_PHASES alone: the first smoke dispatch (run 35155683638) was
# rejected with "unknown phase smoke" by a check that predated lanes, and the
# lane's own contracts never caught it because they tested the library, not
# the runner's gate.
native_e2e_is_selector() {
  local name="${1:?name required}" p
  for p in "${NATIVE_E2E_PHASES[@]}"; do [[ "${name}" == "${p}" ]] && return 0; done
  native_e2e_is_lane "${name}"
}
