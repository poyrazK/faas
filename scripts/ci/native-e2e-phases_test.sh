#!/usr/bin/env bash
# Contracts for the phase partition. The gate's whole value rests on the run
# set being derived from source and covering every metal test; phases add a
# second way to lose a test (assign it nowhere) and a second way to double-run
# one (assign it twice). Both are silent without these checks.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
phases="${repo_root}/scripts/ci/native-e2e-phases.sh"
verdict="${repo_root}/scripts/ci/native-e2e-verdict.sh"

fail() { echo "native e2e phases: $*" >&2; exit 1; }

[[ -r "${phases}" ]] || fail "missing ${phases}"
# shellcheck source=scripts/ci/native-e2e-verdict.sh
source "${verdict}"
# shellcheck source=scripts/ci/native-e2e-phases.sh
source "${phases}"

# 1. The real tree must be an exact partition.
native_e2e_assert_phase_partition "${repo_root}" ||
  fail "the phases are not a partition of the metal suite on this tree"

# 2. Phase count sanity: a collapse to one phase would restore the monolith
#    this split exists to remove.
[[ "${#NATIVE_E2E_PHASES[@]}" -ge 5 ]] ||
  fail "only ${#NATIVE_E2E_PHASES[@]} phases; the split has collapsed"

# 3. Every phase must select at least one test. An empty phase reports "ok"
#    having executed nothing — the retired metal job's failure mode.
for phase in "${NATIVE_E2E_PHASES[@]}"; do
  n="$(native_e2e_phase_tests "${phase}" "${repo_root}" | grep -c . || true)"
  [[ "${n}" -gt 0 ]] || fail "phase ${phase} selects no tests"
done

# The platform benchmark shares the native wake lifecycle. Keep it in the
# wake phase so a new benchmark scenario is derived from its file as well.
native_e2e_phase_tests wake "${repo_root}" | grep -qx TestWakePlatformBenchMetal ||
  fail "platform wake benchmark is no longer included in the wake phase"

native_e2e_phase_tests deploy "${repo_root}" | grep -qx TestProjectWorkerReleaseMetal ||
  fail "project worker release acceptance is missing from the deploy phase"
native_e2e_phase_tests deploy "${repo_root}" | grep -qx TestQueuePushParallelDispatchMetal ||
  fail "push queue delivery acceptance is missing from the deploy phase"

# 4. Every required test must live in some phase, or the gate would require a
#    test it never runs.
for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do
  found=0
  for phase in "${NATIVE_E2E_PHASES[@]}"; do
    if native_e2e_phase_tests "${phase}" "${repo_root}" | grep -qx "${required}"; then
      found=1
      break
    fi
  done
  [[ "${found}" -eq 1 ]] || fail "required test ${required} is in no phase"
done

# Keep the opt-in benchmark in the wake phase; all its test names are derived
# from the file along with the ordinary wake scenarios.
native_e2e_phase_tests wake "${repo_root}" | grep -qx TestWakePlatformBenchMetal ||
  fail "platform wake benchmark is missing from the wake phase"

# The developer live-patch scenario starts from the native deploy path.
# Keep all test names from its file in the deploy phase.
native_e2e_phase_tests deploy "${repo_root}" | grep -qx TestDevLoopMetal ||
  fail "developer loop is missing from the deploy phase"

# Binary functions exercise the source/builder/runner pipeline.
native_e2e_phase_tests build "${repo_root}" | grep -qx TestBinaryFunctionMetal ||
  fail "binary function pipeline is missing from the build phase"

native_e2e_phase_tests streaming "${repo_root}" | grep -qx TestFunctionStreamingMetal ||
  fail "function streaming is missing from the streaming phase"

# 5. The partition assert must actually FAIL on an unassigned metal test.
#    A guard that cannot fail is not a guard — this gate has shipped one before.
probe="$(mktemp -d)"
trap 'rm -rf "${probe}"' EXIT
mkdir -p "${probe}/cmd/e2e"
printf '//go:build metal\n\npackage e2e_test\n\nfunc TestFixtureShape(t *testing.T) {}\n' \
  > "${probe}/cmd/e2e/fixtures_meta_test.go"
printf '//go:build metal\n\npackage e2e_test\n\nfunc TestUnassignedProbe(t *testing.T) {}\n' \
  > "${probe}/cmd/e2e/unassigned_probe_metal_test.go"
if native_e2e_assert_phase_partition "${probe}" >/dev/null 2>&1; then
  fail "the partition assert accepted a metal test assigned to no phase"
fi

# 6. The phase regex must be anchored, or a phase would run every test whose
#    name merely contains one of its own.
rx="$(native_e2e_phase_regex security "${repo_root}")"
[[ "${rx}" == ^\(*\)\$ ]] || fail "phase regex is not anchored: ${rx}"

# 7. A phase that executed nothing must be rejected by the tally.
empty_log="$(mktemp)"
printf 'testing: warning: no tests to run\nPASS\n' > "${empty_log}"
if native_e2e_phase_tally "${empty_log}" probe >/dev/null 2>&1; then
  rm -f "${empty_log}"
  fail "the phase tally accepted a phase that executed no test"
fi
rm -f "${empty_log}"


# 8. Lanes. A lane is hand-picked, so the only way it can silently shrink is a
#    name that no longer matches a metal test; the assert must catch that, and
#    the beta definition must stay what it is.
native_e2e_assert_lanes "${repo_root}" || fail "a lane names a test that is not in the metal suite"
smoke_tests="$(native_e2e_lane_tests smoke "${repo_root}")"
for must in TestSourceDeployWakeMetal TestDeployWakeMetal \
  TestSec11_MemoryMaxFenceEnforced_CrossProcess TestSec11_SeccompFilterEnforced_CrossProcess; do
  grep -qx "${must}" <<<"${smoke_tests}" ||
    fail "smoke lane lost ${must}; it no longer exercises the beta path"
done
# Fixture checks ride along (no microVM), so a broken fixture tree is reported
# before anything boots.
for t in $(native_e2e_phase_tests fixtures "${repo_root}"); do
  grep -qx "${t}" <<<"${smoke_tests}" || fail "smoke lane lost fixtures test ${t}"
done
# And it must stay SMALL: the lane is the answer to the hour-long matrix.
n="$(printf '%s\n' "${smoke_tests}" | grep -c .)"
[[ "${n}" -le 12 ]] || fail "smoke lane has ${n} tests; it is growing back into the full matrix"

exclusive_tests="$(native_e2e_lane_tests exclusive-operations-only "${repo_root}")"
[[ "${exclusive_tests}" == "TestExclusiveOperationFencesRestoredKVMOwnerMetal" ]] ||
  fail "exclusive-operations-only must select exactly the stale-owner KVM test"
[[ "$(native_e2e_lane_regex exclusive-operations-only "${repo_root}")" == \
  '^(TestExclusiveOperationFencesRestoredKVMOwnerMetal)$' ]] ||
  fail "exclusive-operations-only does not build an anchored test filter"
wake_tests="$(native_e2e_phase_tests wake "${repo_root}")"
grep -Fx 'TestExclusiveOperationFencesRestoredKVMOwnerMetal' <<<"${wake_tests}" >/dev/null ||
  fail "exclusive-owner KVM test is no longer included in the wake phase"
native_e2e_is_lane exclusive-operations-only || fail "exclusive-operations-only is not recognised as a lane"
# The managed workflow recovery lane stays isolated and blocking so the new
# Firecracker qualification can run without dispatching the full metal matrix.
managed_operation_tests="$(native_e2e_lane_tests managed-operation-only "${repo_root}")"
[[ "${managed_operation_tests}" == "TestManagedOperationWorkflowMetal" ]] ||
  fail "managed-operation-only must select exactly the managed workflow recovery test"
[[ "$(native_e2e_lane_regex managed-operation-only "${repo_root}")" == \
  '^(TestManagedOperationWorkflowMetal)$' ]] ||
  fail "managed-operation-only does not build an anchored test filter"
native_e2e_is_lane managed-operation-only || fail "managed-operation-only is not recognised as a lane"
# Customer Job Operations derive all scenarios, including future additions.
customer_job_tests="$(native_e2e_lane_tests customer-job-operations-only "${repo_root}")"
for required in TestCustomerJobOperationResultMetal TestCustomerJobOperationRestartMetal \
  TestCustomerJobOperationRecoveryMetal TestCustomerJobOperationCancellationMetal TestCustomerJobOperationDirectUploadMetal; do
  grep -qx "${required}" <<<"${customer_job_tests}" || fail "customer Job lane lost ${required}"
done
native_e2e_is_lane customer-job-operations-only || fail "customer Job selector is not a lane"
rx="$(native_e2e_lane_regex customer-job-operations-only "${repo_root}")"
[[ "${rx}" == ^\(*\)\$ ]] || fail "customer Job regex is not anchored"
customer_job_probe="${probe}/customer-job"
mkdir -p "${customer_job_probe}/cmd/e2e"
cp "${repo_root}/cmd/e2e/customer_job_operations_metal_test.go" "${customer_job_probe}/cmd/e2e/"
printf '\nfunc TestCustomerJobAddedProbe(t *testing.T) {}\n' >> "${customer_job_probe}/cmd/e2e/customer_job_operations_metal_test.go"
native_e2e_lane_tests customer-job-operations-only "${customer_job_probe}" | grep -qx TestCustomerJobAddedProbe || fail "new customer Job scenario was omitted"

customer_workflow_tests="$(native_e2e_lane_tests customer-workflow-operations-only "${repo_root}")"
for required in TestCustomerWorkflowOperationResultMetal TestCustomerWorkflowOperationRecoveryMetal \
  TestCustomerWorkflowOperationCancellationMetal TestCustomerWorkflowOperationDeadlineMetal TestCustomerWorkflowOperationOwnerRevocationMetal \
  TestCustomerWorkflowOperationWaitsAndSignalsMetal TestCustomerWorkflowOperationEventTimeoutMetal TestCustomerWorkflowOperationBudgetCutMetal; do
  grep -qx "${required}" <<<"${customer_workflow_tests}" || fail "customer Workflow lane lost ${required}"
done
native_e2e_is_lane customer-workflow-operations-only || fail "customer Workflow selector is not a lane"
rx="$(native_e2e_lane_regex customer-workflow-operations-only "${repo_root}")"
[[ "${rx}" == ^\(*\)\$ ]] || fail "customer Workflow regex is not anchored"
customer_workflow_probe="${probe}/customer-workflow"
mkdir -p "${customer_workflow_probe}/cmd/e2e"
cp "${repo_root}/cmd/e2e/customer_workflow_operations_metal_test.go" "${customer_workflow_probe}/cmd/e2e/"
printf '\nfunc TestCustomerWorkflowAddedProbe(t *testing.T) {}\n' >> "${customer_workflow_probe}/cmd/e2e/customer_workflow_operations_metal_test.go"
native_e2e_lane_tests customer-workflow-operations-only "${customer_workflow_probe}" | grep -qx TestCustomerWorkflowAddedProbe || fail "new customer Workflow scenario was omitted"

worker_tests="$(native_e2e_lane_tests project-worker-release-only "${repo_root}")"
printf '%s\n' "${worker_tests}" | grep -qx TestProjectWorkerReleaseMetal || fail "project worker lane lost its acceptance scenario"
native_e2e_is_lane project-worker-release-only || fail "project worker selector is not a lane"
rx="$(native_e2e_lane_regex project-worker-release-only "${repo_root}")"
[[ "${rx}" == ^\(*\)\$ ]] || fail "project worker regex is not anchored"
worker_probe="${probe}/project-worker"
mkdir -p "${worker_probe}/cmd/e2e"
cp "${repo_root}/cmd/e2e/project_worker_release_metal_test.go" "${worker_probe}/cmd/e2e/"
printf '\nfunc TestProjectWorkerAddedProbe(t *testing.T) {}\n' >> "${worker_probe}/cmd/e2e/project_worker_release_metal_test.go"
native_e2e_lane_tests project-worker-release-only "${worker_probe}" | grep -qx TestProjectWorkerAddedProbe || fail "new project worker scenario was omitted"
customer_http_tests="$(native_e2e_lane_tests customer-http-operations-only "${repo_root}")"
for required in TestCustomerHTTPOperationResultMetal TestCustomerHTTPOperationRecoveryMetal \
  TestCustomerHTTPOperationCancellationMetal TestCustomerHTTPOperationOwnerRevocationMetal \
  TestCustomerHTTPOperationWebhookAdmissionMetal TestCustomerHTTPOperationDeadlineCutMetal; do
  printf '%s\n' "${customer_http_tests}" | grep -qx "${required}" || fail "customer HTTP lane lost ${required}"
done
native_e2e_is_lane customer-http-operations-only || fail "customer HTTP selector is not a lane"
rx="$(native_e2e_lane_regex customer-http-operations-only "${repo_root}")"
[[ "${rx}" == ^\(*\)\$ ]] || fail "customer HTTP regex is not anchored"
customer_http_probe="${probe}/customer-http"
mkdir -p "${customer_http_probe}/cmd/e2e"
cp "${repo_root}/cmd/e2e/customer_http_operations_metal_test.go" "${customer_http_probe}/cmd/e2e/"
printf '\nfunc TestCustomerHTTPAddedProbe(t *testing.T) {}\n' >> "${customer_http_probe}/cmd/e2e/customer_http_operations_metal_test.go"
native_e2e_lane_tests customer-http-operations-only "${customer_http_probe}" | grep -qx TestCustomerHTTPAddedProbe || fail "new customer HTTP scenario was omitted"
# ADR-20261017102231: a scenario in a sibling customer_http_operations*_metal_test.go file joins the lane.
printf '//go:build metal\n\npackage e2e\n\nfunc TestCustomerHTTPSiblingProbe(t *testing.T) {}\n' > "${customer_http_probe}/cmd/e2e/customer_http_operations_probe_metal_test.go"
native_e2e_lane_tests customer-http-operations-only "${customer_http_probe}" | grep -qx TestCustomerHTTPSiblingProbe || fail "a sibling customer HTTP scenario file was omitted"

# The assert must actually bite: a bogus name fails it.
( NATIVE_E2E_SMOKE_TESTS+=(TestDoesNotExistAnywhere); native_e2e_assert_lanes "${repo_root}" ) 2>/dev/null &&
  fail "native_e2e_assert_lanes accepted a lane naming a nonexistent test"
native_e2e_is_lane smoke || fail "smoke is not recognised as a lane"
native_e2e_is_lane build && fail "a phase was recognised as a lane"

# 9. The runner's selector gate must accept every phase AND every lane, and
#    reject anything else. Run 35155683638 dispatched lane=smoke and the
#    runner died with "unknown phase smoke" before running a single test.
for sel in "${NATIVE_E2E_PHASES[@]}" "${NATIVE_E2E_LANES[@]}"; do
  native_e2e_is_selector "${sel}" || fail "runner selector gate rejects ${sel}"
done
native_e2e_is_selector nonsense && fail "runner selector gate accepted a bogus name"
grep -q 'native_e2e_is_selector "${phase}"' "${repo_root}/scripts/ci/run-native-e2e.sh" ||
  fail "run-native-e2e.sh does not validate FAAS_E2E_PHASE with native_e2e_is_selector; a lane would be rejected as an unknown phase"

# Container qualification derives complete files and rejects lost coverage.
container_tests="$(native_e2e_lane_tests containers "${repo_root}")"
for must in TestDirectOCIFullRootfsMetal TestDirectOCIProcessContractMetal TestDirectOCIPort3000Metal \
  TestDirectOCIAutoscaleScaleToZeroMetal TestAfterRestoreMetal \
  TestBeforeCheckpointMetal TestTCPIngressMetal TestDeployHealthcheckMetal \
  TestE2E_Streaming_Metal_H2CInnerLeg \
  TestSec11_MemoryMaxFenceEnforced_CrossProcess TestSec11_SeccompFilterEnforced_CrossProcess; do
  grep -qx "${must}" <<<"${container_tests}" || fail "container lane lost ${must}"
done
rx="$(native_e2e_lane_regex containers "${repo_root}")"
[[ "${rx}" == ^\(*\)\$ ]] || fail "container lane regex is not anchored"
# A new test in a selected file must be included automatically.
container_probe="${probe}/container"
mkdir -p "${container_probe}/cmd"
cp -R "${repo_root}/cmd/e2e" "${container_probe}/cmd/e2e"
printf '\nfunc TestContainerAddedProbe(t *testing.T) {}\n' >> "${container_probe}/cmd/e2e/direct_oci_fullrootfs_metal_test.go"
native_e2e_lane_tests containers "${container_probe}" | grep -qx TestContainerAddedProbe || fail "new container test was omitted"

# 10. The stale-jail reaper removes app-instance chroots as well as build ones,
#     and nothing outside firecracker/ or firecracker-v*/. Two app chroots that survived a node
#     reboot blocked smoke run 35206846279 before a single test ran.
jail_tmp="$(mktemp -d)"
mkdir -p "${jail_tmp}/firecracker-v1.7.0-x86_64/645b161d-79ef-4a24-adcb-8c96a48e57b1/root" \
         "${jail_tmp}/firecracker-v1.7.0-x86_64/build-01a0ac83/root" \
         "${jail_tmp}/firecracker/app-unversioned/root" \
         "${jail_tmp}/firecracker/build-unversioned/root" \
         "${jail_tmp}/keep-me"
# shellcheck source=scripts/ci/native-e2e-reap.sh
source "${repo_root}/scripts/ci/native-e2e-reap.sh"
reap_stale_jails "${jail_tmp}"
[[ ! -e "${jail_tmp}/firecracker-v1.7.0-x86_64/645b161d-79ef-4a24-adcb-8c96a48e57b1" ]] ||
  fail "reap_stale_jails left an app-instance chroot; the next run's pre-flight leakcheck would refuse to start"
[[ ! -e "${jail_tmp}/firecracker-v1.7.0-x86_64/build-01a0ac83" ]] || fail "reap_stale_jails left a build chroot"
[[ ! -e "${jail_tmp}/firecracker/app-unversioned" ]] || fail "reap_stale_jails left an unversioned app chroot"
[[ ! -e "${jail_tmp}/firecracker/build-unversioned" ]] || fail "reap_stale_jails left an unversioned build chroot"
[[ -d "${jail_tmp}/keep-me" ]] || fail "reap_stale_jails removed something outside the Firecracker jail parents"
rm -rf "${jail_tmp}"
grep -qE '^reap_test_microvms$' "${repo_root}/scripts/ci/run-native-e2e.sh" ||
  fail "run-native-e2e.sh does not reap before the pre-flight leakcheck; a previous run's leftovers block this one"

# 11. reap_test_microvms must survive errexit with nothing to reap. Stub every
#     host command it calls so the test touches no real process, netns, mount
#     or cgroup, and make each stub return the "nothing matched" status.
#     Run in a SEPARATE `bash -e` process: inside `( ... ) || fail` or an `if`
#     condition, bash ignores errexit, and the check could never trip.
set +e
bash -e -c '
  source "$1"
  pgrep()  { return 1; }; pkill() { return 1; }; kill() { return 1; }
  ip()     { return 1; }; umount() { return 1; }; rmdir() { return 1; }
  awk()    { return 0; }; sleep() { :; }
  FAAS_E2E_JAIL_ROOT="$(mktemp -d)"
  reap_test_microvms
' _ "${repo_root}/scripts/ci/native-e2e-reap.sh" >/dev/null 2>&1
reap_rc=$?
set -e
[[ "${reap_rc}" -eq 0 ]] ||
  fail "reap_test_microvms exits ${reap_rc} under set -e when there is nothing to reap; the pre-flight would abort silently"

# Exercise the process selectors and the real mount parser without touching
# host resources. The unversioned layout is what jailer creates on the node.
reap_probe="${probe}/reap"
mkdir -p "${reap_probe}/jail/firecracker/app/root" "${reap_probe}/jail/firecracker-v1.7.0/build/root"
printf 'bind %s none rw 0 0\n' \
  "${reap_probe}/jail/firecracker/app/root/snap-in-mem" \
  "${reap_probe}/jail/firecracker-v1.7.0/build/root/drive" \
  "${reap_probe}/other/firecracker/app/root/keep" > "${reap_probe}/mounts"
bash -e -c '
  source "$1"
  export FAAS_E2E_JAIL_ROOT="$2/jail"
  calls="$2/calls"
  mount_fixture="$2/mounts"
  pgrep() {
    [[ "$1" == -x ]] || exit 2
    for name in firecracker firecracker-v1.7.0-x86_64 inspection-firecracker vmmd; do
      if printf "%s\n" "$name" | grep -Eq "^($2)$"; then
        printf "%s\n" "$name" >> "$calls"
        printf "12345\n"
      fi
    done
  }
  pkill() {
    [[ "$1 $2" == "-KILL -x" ]] && [[ "$3" == "firecracker(-v[0-9].*)?" ]] || exit 2
    printf "kill-fallback\n" >> "$calls"
  }
  kill() { :; }; sleep() { :; }; ip() { return 1; }; rmdir() { return 1; }
  umount() { printf "mount %s\n" "$2" >> "$calls"; }
  awk() {
    if [[ "${!#}" == /proc/mounts ]]; then
      command awk "${@:1:$#-1}" "$mount_fixture"
    else
      command awk "$@"
    fi
  }
  reap_test_microvms
' _ "${repo_root}/scripts/ci/native-e2e-reap.sh" "${reap_probe}" >/dev/null
grep -qx firecracker "${reap_probe}/calls" || fail "reaper misses unversioned Firecracker processes"
grep -qx firecracker-v1.7.0-x86_64 "${reap_probe}/calls" || fail "reaper misses versioned Firecracker processes"
grep -qx kill-fallback "${reap_probe}/calls" || fail "reaper has no exact-name kill fallback"
[[ "$(grep -c '^mount ' "${reap_probe}/calls")" -eq 2 ]] || fail "reaper missed a jail mount or touched an unrelated mount"
grep -qx "mount ${reap_probe}/jail/firecracker/app/root/snap-in-mem" "${reap_probe}/calls" || fail "reaper misses unversioned jail mounts"
! grep -qE 'inspection|vmmd|/other/' "${reap_probe}/calls" || fail "reaper selected unrelated resources"

echo "native e2e phase contracts OK (${#NATIVE_E2E_PHASES[@]} phases, ${#NATIVE_E2E_LANES[@]} lanes)"

# Slow migration qualification is selected explicitly, with its extra build tag.
contract_probe="$(mktemp -d)"
mkdir -p "${contract_probe}/cmd/e2e"
cp "${repo_root}/cmd/e2e/project_migration_contract_metal_test.go" "${contract_probe}/cmd/e2e/"
printf '\nfunc TestProjectContractAddedProbe(t *testing.T) {}\n' >> "${contract_probe}/cmd/e2e/project_migration_contract_metal_test.go"
native_e2e_lane_tests project-migration-contract-only "${contract_probe}" | grep -qx TestProjectContractAddedProbe || fail "new contract scenario was omitted"
sed -i '1c//go:build metal' "${contract_probe}/cmd/e2e/project_migration_contract_metal_test.go"
if native_e2e_lane_tests project-migration-contract-only "${contract_probe}" >/dev/null 2>&1; then fail "contract lane accepted a lost acceptance build tag"; fi
rm -rf "${contract_probe}"
echo "contract lane selection and build tag checks OK"

sql_probe="$(mktemp -d)"
mkdir -p "${sql_probe}/cmd/e2e"
cp "${repo_root}/cmd/e2e/project_migration_sql_metal_test.go" "${sql_probe}/cmd/e2e/"
printf '\nfunc TestProjectSQLAddedProbe(t *testing.T) {}\n' >> "${sql_probe}/cmd/e2e/project_migration_sql_metal_test.go"
native_e2e_lane_tests project-migration-sql-only "${sql_probe}" | grep -qx TestProjectSQLAddedProbe || fail 'new SQL scenario was omitted'
sed -i '1c//go:build metal' "${sql_probe}/cmd/e2e/project_migration_sql_metal_test.go"
if native_e2e_lane_tests project-migration-sql-only "${sql_probe}" >/dev/null 2>&1; then fail 'SQL lane accepted a lost acceptance build tag';fi
rm -rf "${sql_probe}"
echo 'SQL lane selection and build tag checks OK'

mapfile -t http_tests < <(native_e2e_lane_tests project-progressive-http-only "${repo_root}")
[[ ${#http_tests[@]} == 9 ]] || fail "HTTP lane must select all nine scenarios"

mapfile -t concurrent_tests < <(native_e2e_lane_tests project-concurrent-release-only "${repo_root}")
[[ ${#concurrent_tests[@]} == 3 ]] || fail "concurrent lane must select three ownership races"

mapfile -t secret_release_tests < <(native_e2e_lane_tests project-release-secrets-only "${repo_root}")
[[ ${#secret_release_tests[@]} == 2 ]] || fail "secret lane must require promotion and rollback"

mapfile -t promotion_tests < <(native_e2e_lane_tests project-environment-promotion-only "${repo_root}")
[[ ${#promotion_tests[@]} == 11 ]] || fail "environment promotion lane must select metadata, success, rollback, controls, load, admission, four migration outcomes and publication crashes"
promotion_probe="$(mktemp -d)"
mkdir -p "${promotion_probe}/cmd/e2e"
cp "${repo_root}/cmd/e2e/project_environment_promotion"{,_fixture,_controls,_concurrent,_admission,_migrations,_publication}"_metal_test.go" "${promotion_probe}/cmd/e2e/"
printf '\nfunc TestProjectPromotionAddedProbe(t *testing.T) {}\n' >> "${promotion_probe}/cmd/e2e/project_environment_promotion_metal_test.go"
native_e2e_lane_tests project-environment-promotion-only "${promotion_probe}" | grep -qx TestProjectPromotionAddedProbe || fail "new promotion scenario was omitted"
sed -i '1c//go:build metal' "${promotion_probe}/cmd/e2e/project_environment_promotion_metal_test.go"
if native_e2e_lane_tests project-environment-promotion-only "${promotion_probe}" >/dev/null 2>&1; then fail "promotion lane accepted a lost acceptance build tag"; fi
rm -rf "${promotion_probe}"
echo 'environment promotion lane selection and build tag checks OK'

partition="$(for group in promotion-core promotion-controls promotion-load promotion-admission promotion-migrations promotion-publication; do native_e2e_promotion_group_tests "$group" "$repo_root"; done)"
[[ "$(printf '%s\n' "$partition" | sort)" == "$(printf '%s\n' "${promotion_tests[@]}" | sort)" ]] || fail 'promotion partitions lost or duplicated a scenario'
if native_e2e_promotion_group_tests unknown "$repo_root" >/dev/null 2>&1;then fail 'unknown promotion group accepted';fi
echo 'promotion group partition checks OK'

graph_tests="$(native_e2e_promotion_group_tests promotion-service-graph "$repo_root")"
[[ "$(printf '%s\n' "$graph_tests" | wc -l)" == 2 ]] || { echo 'service graph group lost exact inventory' >&2;exit 1; }
if printf '%s\n' "$partition" | grep -F 'PromotionServiceGraph'; then echo 'optional service graph widened base inventory' >&2;exit 1;fi
echo 'optional service graph partition checks OK'

coordinated_tests="$(native_e2e_promotion_group_tests promotion-coordinated "$repo_root")"
[[ "$(printf '%s\n' "$coordinated_tests" | wc -l)" == 4 ]] || { echo 'coordinated group lost exact inventory' >&2;exit 1; }
for test in TestProjectEnvironmentPromotionCoordinatedFixtureMeta TestProjectEnvironmentPromotionCoordinatedSuccessMetal TestProjectEnvironmentPromotionCoordinatedRollbackMetal TestProjectEnvironmentPromotionCoordinatedResolveMetal;do
 printf '%s\n' "$coordinated_tests" | grep -Fxq "$test" || { echo "coordinated group missing $test" >&2;exit 1; }
done
if printf '%s\n' "$partition" | grep -F 'PromotionCoordinated'; then echo 'coordinated option widened base inventory' >&2;exit 1;fi
echo 'optional coordinated partition checks OK'
