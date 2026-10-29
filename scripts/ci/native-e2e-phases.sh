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
  financial
  jobs
)

# native_e2e_phase_files echoes the test files belonging to a phase, one per
# line. Globs are matched against cmd/e2e; a glob that matches nothing is a
# hard error via the partition assert, not a silent empty phase.
native_e2e_phase_files() {
  case "${1:?phase name required}" in
    # Shape/contract checks over the fixture apps. No microVM, seconds to run —
    # first so a broken fixture tree is reported before anything boots.
    fixtures) printf '%s\n' fixtures_meta_test.go project_worker_release_fixture_meta_metal_test.go ;;
    # Source -> builderd -> builder microVM -> OCI image. The most expensive
    # phase by far (a stalled build rides out its 10-minute budget), and the
    # one everything below depends on.
    build) printf '%s\n' build_metal_test.go catalog_runtime_metal_test.go binary_function_metal_test.go \
      dockerfile_healthcheck_metal_test.go ;;
    # Deploy and safe-release paths: image, canary progression, healthcheck,
    # rollback, port override and secrets.
    deploy) printf '%s\n' \
      app_task_interactive_metal_test.go \
      deploy_healthcheck_metal_test.go \
      image_process_identity_metal_test.go \
      project_worker_release_metal_test.go \
      project_progressive_release_metal_test.go \
      project_progressive_http_metal_test.go \
      project_progressive_service_graph_metal_test.go \
      project_progressive_concurrent_metal_test.go \
      project_rollback_retention_metal_test.go \
      project_release_secrets_metal_test.go \
      queue_push_parallel_metal_test.go \
      dev_loop_metal_test.go \
      dev_watch_metal_test.go \
      environment_gitops_native_metal_test.go \
      environment_gitops_source_jobs_native_metal_test.go \
      deploy_override_port_metal_test.go \
      deploy_wake_metal_test.go \
      safe_release_drill_metal_test.go \
      release_guards_drill_metal_test.go \
      release_guards_ga_drill_metal_test.go \
      dev_loop_metal_test.go \
      direct_oci_autoscale_metal_test.go \
      direct_oci_fullrootfs_metal_test.go \
      direct_oci_port_metal_test.go \
      real_image_smoke_metal_test.go \
      function_concurrency_metal_test.go \
      guest_tracing_metal_test.go \
      single_process_oom_metal_test.go \
      profile_capture_metal_test.go \
      profile_capture_pprof_metal_test.go \
      profile_capture_pressure_metal_test.go \
      image_process_identity_metal_test.go \
      workload_identity_metal_test.go \
      image_process_identity_metal_test.go \
      source_deploy_wake_metal_test.go \
      shared_runtime_rollback_metal_test.go \
      secrets_image_deploy_e2e_test.go \
      object_storage_native_metal_test.go \
      queue_binding_native_metal_test.go \
      queue_push_parallel_metal_test.go \
      private_service_tcp_metal_test.go \
      scenario_tcp_chaos_metal_test.go \
      tcp_ingress_metal_test.go udp_ingress_metal_test.go ;;
    # Wake scheduling, native Flags cache refresh after VM restore, and service
    # calls and private TCP that wake a just-parked target (ADR-1131..1134).
    # The platform benchmark remains opt-in via FAAS_WAKE_PLATFORM_BENCH_CYCLES.
    wake) printf '%s\n' \
      wake_timeline_metal_test.go \
      frozen_tier_metal_test.go \
      quiet_pause_billing_metal_test.go \
      worker_frozen_metal_test.go \
      worker_scale_from_zero_metal_test.go \
      wake_platform_bench_metal_test.go \
      wake_burst_metal_test.go \
      wake_platform_bench_metal_test.go \
      after_restore_metal_test.go \
      exclusive_operations_restore_metal_test.go \
      durable_entities_restore_metal_test.go \
      managed_operation_workflow_metal_test.go \
      customer_workflow_operations_metal_test.go \
      customer_workflow_operations_v1_metal_test.go \
      customer_workflow_operations_features_metal_test.go \
      customer_http_operations_metal_test.go \
      customer_http_operations_v1_metal_test.go \
      customer_http_operations_features_metal_test.go \
      feature_flags_native_restore_metal_test.go \
      before_checkpoint_metal_test.go \
      fleet_wake_dedup_e2e_test.go \
      private_service_tcp_stale_metal_test.go \
      private_tcp_wake_ahead_metal_test.go \
      service_stale_endpoint_metal_test.go \
      wake_platform_bench_metal_test.go \
      fork_metal_test.go \
      live_fork_metal_test.go \
      crash_snapshot_metal_test.go \
      crash_snapshot_sdk_metal_test.go \
      vmmd_restart_metal_test.go \
      cpu_fairness_test.go ;;
    # Response streaming and the h2c/gRPC inner leg (G19.3).
    streaming) printf '%s\n' streaming_metal_test.go bridge_h2c_terminator_metal_test.go function_streaming_metal_test.go instance_bridge_sessions_metal_test.go ;;
    # App log delivery (SSE).
    logs) printf '%s\n' logs_e2e_test.go logs_archive_metal_test.go ;;
    # Spec §11 ship-blocking fences. Small, and must never be skipped quietly.
    security) printf '%s\n' sec11_memory_max_e2e_test.go sec11_seccomp_e2e_test.go ;;
    # Multi-node control plane: recovery arbiter, drain, heartbeat drills.
    twonode) printf '%s\n' twonode_failure_safe_metal_test.go twonode_runbook_test.go ;;
    financial) printf '%s\n' financial_system_metal_test.go financial_budget_preview_stops_metal_test.go ;;
    # Real jobs harness and scratch-image workload. This phase remains
    # non-blocking until it passes on the dedicated KVM runner.
    jobs) printf '%s\n' jobs_metal_test.go customer_job_operations_metal_test.go \
      customer_job_operations_features_metal_test.go \
      image_runs_metal_test.go job_task_logs_metal_test.go image_run_interactive_metal_test.go ;;
    *) echo "native-e2e-phases: unknown phase: $1" >&2; return 1 ;;
  esac
}

# native_e2e_sibling_files lists the cmd/e2e files matching a glob as the
# comma-separated --files value native_e2e_inventory takes.
native_e2e_sibling_files() {
  local root="${1:?root required}" pattern="${2:?pattern required}" f out=""
  for f in "${root}"/cmd/e2e/${pattern}; do
    [[ -f "${f}" ]] && out="${out:+${out},}${f##*/}"
  done
  printf '%s' "${out}"
}

# Derive top-level tests from the phase files using the same Go build context
# as the complete platform inventory. Specialized binding tags have their own
# strict runners and never leak into a plain-metal phase through text matching.
native_e2e_phase_tests() {
  local phase="${1:?phase name required}" root="${2:-.}" files
  files="$(native_e2e_phase_files "$phase")" || return 1
  native_e2e_inventory "$root" --files "${files//$'\n'/,}"
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
  all_tests="$(native_e2e_metal_tests "${root}")" || return 1

  seen=""
  dupes=""
  for phase in "${NATIVE_E2E_PHASES[@]}"; do
    phase_tests="$(native_e2e_phase_tests "${phase}" "${root}")" || return 1
    while IFS= read -r t; do
      [[ -n "${t}" ]] || continue
      if grep -Fxq -- "${t}" <<<"${seen}"; then
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
# exclusive-operations-only — the stale-owner snapshot/restore fence on
# dedicated KVM, without waiting for the platform-wide matrix. The final
# verdict requires its selected test to PASS; the runner still performs the
# normal preflight, service restoration, and leakcheck.
# managed-operation-only — source deployment, guest process death, in-place
# managed workflow retry, and signed effect delivery on dedicated KVM.
# customer-job-operations-only derives every Customer Job Operations scenario.
# Missing or skipped scenarios fail its dedicated blocking verdict.
# project-worker-release-only qualifies mixed HTTP/push-worker cutover,
# restart recovery, probe rollback, startup refusal and bounded retirement.
# customer-workflow-operations-only derives every native workflow scenario;
# its selected tests are blocking independently of the full matrix's phases.
# customer-http-operations-only derives every ordinary HTTP Operations scenario
# (ADR-521); it is the native gate before a customer preview cohort opens.
NATIVE_E2E_LANES=(smoke containers exclusive-operations-only managed-operation-only customer-job-operations-only customer-workflow-operations-only customer-http-operations-only project-worker-release-only project-progressive-release-only project-progressive-http-only project-concurrent-release-only project-rollback-retention-only project-release-secrets-only project-migration-contract-only project-migration-sql-only project-environment-promotion-only financial-system)

# NATIVE_E2E_SMOKE_TESTS lists the smoke lane by NAME. Hand-picked on purpose
# (see above); native_e2e_assert_lanes below fails if any name is not a real
# metal test, so a rename or a lost build tag cannot quietly shrink the lane.
NATIVE_E2E_SMOKE_TESTS=(
  TestSourceDeployWakeMetal
  TestDeployWakeMetal
  TestSec11_MemoryMaxFenceEnforced_CrossProcess
  TestSec11_SeccompFilterEnforced_CrossProcess
)

NATIVE_E2E_EXCLUSIVE_OPERATIONS_TESTS=(
  TestExclusiveOperationFencesRestoredKVMOwnerMetal
)

NATIVE_E2E_MANAGED_OPERATION_TESTS=(
  TestManagedOperationWorkflowMetal
)

# native_e2e_lane_tests echoes a lane's tests, one per line. smoke is the
# explicit list plus every fixtures-phase test (no microVM, seconds).
native_e2e_lane_tests() {
  local lane="${1:?lane name required}" root="${2:-.}" file
  case "${lane}" in
    smoke)
      {
        native_e2e_phase_tests fixtures "${root}"
        printf '%s\n' "${NATIVE_E2E_SMOKE_TESTS[@]}"
      } | sort -u
      ;;
    containers)
      native_e2e_inventory "$root" --files 'direct_oci_fullrootfs_metal_test.go,direct_oci_port_metal_test.go,direct_oci_autoscale_metal_test.go,deploy_healthcheck_metal_test.go,after_restore_metal_test.go,before_checkpoint_metal_test.go,tcp_ingress_metal_test.go,udp_ingress_metal_test.go,streaming_metal_test.go,sec11_memory_max_e2e_test.go,sec11_seccomp_e2e_test.go'
      ;;
    financial-system)
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/financial_system_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    exclusive-operations-only)
      printf '%s\n' "${NATIVE_E2E_EXCLUSIVE_OPERATIONS_TESTS[@]}"
      ;;
    managed-operation-only)
      printf '%s\n' "${NATIVE_E2E_MANAGED_OPERATION_TESTS[@]}"
      ;;
    customer-job-operations-only)
      # Every customer_job_operations*_metal_test.go file, as the HTTP and
      # workflow lanes do, so feature scenarios join without a selector edit.
      native_e2e_inventory "$root" --files "$(native_e2e_sibling_files "$root" 'customer_job_operations*_metal_test.go')"
      ;;
    customer-workflow-operations-only)
      # Every customer_workflow_operations*_metal_test.go file, so the
      # ADR-20261017102231 Stage 2 scenarios join the lane without a selector edit.
      native_e2e_inventory "$root" --files "$(native_e2e_sibling_files "$root" 'customer_workflow_operations*_metal_test.go')"
      ;;
    customer-http-operations-only)
      native_e2e_inventory "$root" --files "$(native_e2e_sibling_files "$root" 'customer_http_operations*_metal_test.go')"
      ;;
    project-environment-promotion-only)
      native_e2e_promotion_group_tests "${3:-all}" "$root"
      ;;
    project-migration-sql-only)
      grep -qx '//go:build managed_postgres_contract && contractacceptance && metal' "${root}/cmd/e2e/project_migration_sql_metal_test.go" || return 1
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_migration_sql_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    project-migration-contract-only)
      grep -qx '//go:build contractacceptance && metal' "${root}/cmd/e2e/project_migration_contract_metal_test.go" || return 1
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_migration_contract_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    project-release-secrets-only)
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_release_secrets_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    project-rollback-retention-only)
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_rollback_retention_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    project-concurrent-release-only)
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_progressive_concurrent_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    project-progressive-http-only)
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_progressive_http_metal_test.go" "${root}/cmd/e2e/project_progressive_service_graph_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    project-progressive-release-only)
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_progressive_release_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    project-worker-release-only)
      grep -hoE '^func Test[A-Za-z0-9_]+\(' "${root}/cmd/e2e/project_worker_release_metal_test.go" |
        sed -E 's/^func //; s/\($//' | sort -u
      ;;
    *) echo "native-e2e-phases: unknown lane: ${lane}" >&2; return 1 ;;
  esac
}

# Promotion jobs share one admission scope, but run bounded native partitions.
# Derive names from every assigned file; an added test cannot silently disappear.
native_e2e_promotion_group_tests() {
  local group="${1:?promotion group required}" root="${2:-.}" file
  local files=()
  case "$group" in
    all) files=(project_environment_promotion_metal_test.go project_environment_promotion_fixture_metal_test.go project_environment_promotion_controls_metal_test.go project_environment_promotion_concurrent_metal_test.go project_environment_promotion_admission_metal_test.go project_environment_promotion_migrations_metal_test.go project_environment_promotion_publication_metal_test.go) ;;
    promotion-core) files=(project_environment_promotion_metal_test.go project_environment_promotion_fixture_metal_test.go) ;;
    promotion-controls) files=(project_environment_promotion_controls_metal_test.go) ;;
    promotion-load) files=(project_environment_promotion_concurrent_metal_test.go) ;;
    promotion-migrations) files=(project_environment_promotion_migrations_metal_test.go) ;;
    promotion-publication) files=(project_environment_promotion_publication_metal_test.go) ;;
    promotion-coordinated) files=(project_environment_promotion_coordinated_metal_test.go project_environment_promotion_coordinated_fixture_metal_test.go) ;;
    promotion-service-graph) files=(project_environment_promotion_service_graph_metal_test.go project_environment_promotion_service_graph_fixture_metal_test.go) ;;
    promotion-admission) files=(project_environment_promotion_admission_metal_test.go) ;;
    *) echo "unknown promotion group: $group" >&2; return 1 ;;
  esac
  for file in "${files[@]}"; do
    grep -qx '//go:build promotionacceptance && metal' "$root/cmd/e2e/$file" || return 1
    grep -hoE '^func Test[A-Za-z0-9_]+\(' "$root/cmd/e2e/$file" | sed -E 's/^func //; s/\($//'
  done | sort -u
}

# native_e2e_lane_regex builds the -run anchor for a lane. Refuses an empty
# lane for the same reason native_e2e_phase_regex does.
native_e2e_lane_regex() {
  local lane="${1:?lane name required}" root="${2:-.}" tests
  tests="$(native_e2e_lane_tests "${lane}" "${root}" "${3:-all}")"
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
  local root="${1:-.}" all_tests lane selected t rc=0
  all_tests="$(native_e2e_metal_tests "${root}")" || return 1
  for lane in "${NATIVE_E2E_LANES[@]}"; do
    selected="$(native_e2e_lane_tests "${lane}" "${root}")" || return 1
    [[ -n "${selected}" ]] || return 1
    if [[ "${lane}" == project-migration-contract-only || "${lane}" == project-migration-sql-only || "${lane}" == project-environment-promotion-only ]]; then
      all_tests="$(native_e2e_metal_tests "${root}"; native_e2e_lane_tests "${lane}" "${root}")"
    fi
    while IFS= read -r t; do
      [[ -n "${t}" ]] || continue
      grep -qx -- "${t}" <<<"${all_tests}" || {
        echo "native-e2e-phases: lane ${lane} names ${t}, which is not a metal-tagged test (renamed, or lost its build tag)" >&2
        rc=1
      }
    done <<<"${selected}"
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

# Full qualification runs in two serialized jobs, each with independent cleanup.
# Keep this partition shared with budget checks and the combined verdict.
native_e2e_group_phases() {
  case "${1:?group required}" in
    source) printf '%s\n' fixtures build deploy ;;
    runtime) printf '%s\n' wake streaming logs security twonode jobs ;;
    *) echo "unknown native phase group: $1" >&2; return 1 ;;
  esac
}
