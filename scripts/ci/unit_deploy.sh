#!/usr/bin/env bash
# Host steps of a per-unit deploy (docs/adr/1650-per-unit-control-plane-releases.md),
# shared by cd-controlplane.yml's deploy-units job and cd-edge-units.yml.
# Every step reads a plan from scripts/ci/plan_unit_deploy.py.
#
#   unit_deploy.sh state                         print the host's active release as JSON
#   unit_deploy.sh stage PLAN UNITS_DIR DEPLOYCTL  copy the plan's files to the host; print the stage dir
#   unit_deploy.sh script PLAN STAGE OUT         write the activation script (runs on the host)
#   unit_deploy.sh run SCRIPT                    run a script on the host as root
#   unit_deploy.sh now                           print the gate host's clock (unix seconds)
#   unit_deploy.sh gate PLAN DEPLOYED_AT KIND    run the SLO gate for the plan's units; KIND is
#                                                canary or follower (scripts/ci/release_units.json "gates")
#   unit_deploy.sh rollback PLAN STAGE           put the plan's services back on the release that was
#                                                active before the plan's
#   unit_deploy.sh cleanup STAGE                 remove the stage dir
#
# The control plane's host-configuration step
# (docs/adr/20261018133100-host-configuration-lane.md):
#
#   unit_deploy.sh host-state                    print the full release and the host-config record as JSON
#   unit_deploy.sh host-manifest STATE OUT       copy the active release's production manifest, checked
#                                                against the manifest_hash host-state printed
#   unit_deploy.sh host-stage DEPLOYCTL          copy deployctl to the host; print the stage dir
#   unit_deploy.sh host-restart-script STAGE OUT write the script (runs on the host) that restarts the
#                                                daemons whose start inputs changed since they started
#                                                (deployctl restart-changed), saving its report in STAGE
#   unit_deploy.sh host-restarted STAGE          print that report
#   unit_deploy.sh host-record RECORD CONTRACT   record the converged role hashes and the bootstrap
#                                                contract (after the SLO gate)
#
# The deploy host is UNIT_SSH_TARGET (user@host) with UNIT_SSH_KEY,
# UNIT_SSH_KNOWN_HOSTS (default ~/.ssh/known_hosts) and UNIT_SSH_PORT
# (default 22); a non-root user runs host commands through sudo -n. The SLO
# gate runs where Prometheus listens, the control plane: UNIT_GATE_SSH_TARGET,
# UNIT_GATE_SSH_KEY and UNIT_GATE_KNOWN_HOSTS default to the deploy host's,
# and PROMETHEUS_URL to the control plane's http://127.0.0.1:9095.
#
# gate exits 0 to promote, 1 when any unit breached (roll back), and 3 when
# any gate was inconclusive or misconfigured: no evidence is not a pass.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"

die() {
  echo "unit_deploy: $*" >&2
  exit 2
}

ssh_opts() { # prefix: UNIT or UNIT_GATE
  local key_var="${1}_SSH_KEY" known_var="${1}_SSH_KNOWN_HOSTS" port_var="${1}_SSH_PORT"
  local key="${!key_var:-${UNIT_SSH_KEY:?set UNIT_SSH_KEY}}"
  local known="${!known_var:-${UNIT_SSH_KNOWN_HOSTS:-$HOME/.ssh/known_hosts}}"
  local port="${!port_var:-${UNIT_SSH_PORT:-22}}"
  printf '%s\n' -i "$key" -p "$port" -o BatchMode=yes -o IdentitiesOnly=yes \
    -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$known"
}

deploy_target() { printf '%s' "${UNIT_SSH_TARGET:?set UNIT_SSH_TARGET}"; }
gate_target() { printf '%s' "${UNIT_GATE_SSH_TARGET:-$(deploy_target)}"; }

# as_root TARGET: the remote command prefix for root on TARGET.
as_root() { [[ "${1%%@*}" == root ]] && printf 'bash' || printf 'sudo -n bash'; }

remote() { # remote PREFIX TARGET COMMAND...  (stdin passes through)
  local prefix="$1" target="$2"
  shift 2
  local -a opts=()
  local opt
  while IFS= read -r opt; do opts+=("$opt"); done < <(ssh_opts "$prefix")
  # Callers build remote commands only from values validated above them.
  # shellcheck disable=SC2029
  ssh "${opts[@]}" "$target" "$@"
}

copy() { # copy PREFIX SRC... TARGET:DEST
  local prefix="$1"
  shift
  local -a opts=()
  local opt
  while IFS= read -r opt; do opts+=("$opt"); done < <(ssh_opts "$prefix")
  # scp takes the port as -P.
  for i in "${!opts[@]}"; do [[ "${opts[$i]}" != -p ]] || opts[i]=-P; done
  scp -q "${opts[@]}" "$@"
}

plan_field() { jq -er "$2" "$1"; }

cmd_state() {
  local target
  target="$(deploy_target)"
  remote UNIT "$target" "$(as_root "$target") -se" <<'REMOTE'
set -euo pipefail
current="$(readlink -f /opt/faas/current)"
test -s "$current/release-manifest.json"
versions=null
if [[ -s "$current/unit-versions.json" ]]; then
  versions="$(cat "$current/unit-versions.json")"
fi
jq -n --arg current "$(basename "$current")" \
  --argjson release "$(cat "$current/release-manifest.json")" \
  --argjson versions "$versions" \
  '{current: $current, base_git_sha: $release.git_sha, unit_versions: $versions}'
REMOTE
}

cmd_stage() {
  local plan="$1" units="$2" deployctl="$3" target local_stage remote_stage
  target="$(deploy_target)"
  local_stage="$(mktemp -d "${RUNNER_TEMP:-/tmp}/unit-stage.XXXXXX")"
  trap 'rm -rf -- "$local_stage"' RETURN
  install -d -m 0755 "$local_stage/bin"
  while read -r unit; do
    [[ -n "$unit" ]] || continue
    install -m 0755 "$units/bin/$unit" "$local_stage/bin/$unit"
  done < <(jq -r '.units[]' "$plan")
  if [[ "$(plan_field "$plan" .action)" == deploy ]]; then
    install -m 0644 "$(dirname "$plan")/unit-versions.json" "$local_stage/unit-versions.json"
  fi
  # The data step: deployctl deploy runs the overlay's bin/migrate before it
  # activates anything (docs/adr/20261018084150-unit-lane-expand-migrations.md).
  if jq -e '.replace["bin/migrate"]' "$plan" >/dev/null; then
    install -m 0755 "$units/tools/migrate" "$local_stage/bin/migrate"
  fi
  install -m 0755 "$deployctl" "$local_stage/bin/deployctl"
  install -m 0755 "$repo_root/scripts/unit-release-verify.sh" "$local_stage/unit-release-verify.sh"
  tar --create --gzip --file "$local_stage.tar.gz" --directory "$local_stage" .
  remote_stage="$(remote UNIT "$target" 'mktemp -d /var/tmp/gregale-units.XXXXXXXX')"
  [[ "$remote_stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "unexpected remote stage dir: $remote_stage"
  copy UNIT "$local_stage.tar.gz" "$target:$remote_stage/stage.tar.gz"
  rm -f -- "$local_stage.tar.gz"
  remote UNIT "$target" "tar --extract --gzip --file '$remote_stage/stage.tar.gz' --directory '$remote_stage' --no-same-owner && rm -f -- '$remote_stage/stage.tar.gz'"
  printf '%s\n' "$remote_stage"
}

# The activation script runs on the host as root. It assembles the overlay
# (deploy), activates it restarting only the plan's services, and gates each
# unit's processes and readiness; a failed unit gate rolls straight back.
# deployctl deploy applies the overlay's migrations first, so a failed
# migration stops the script before any restart.
cmd_script() {
  local plan="$1" stage="$2" out="$3" release_id expect only commit release_dir files="" specs=""
  release_id="$(plan_field "$plan" .release_id)"
  expect="$(plan_field "$plan" .expect_current)"
  only="$(jq -r '.only | join(",")' "$plan")"
  [[ "$release_id" =~ ^[0-9a-f]{40}$ && "$expect" =~ ^[0-9a-f]{40}$ ]] || die "plan release ids must be 40 hex: $release_id, $expect"
  [[ "$stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "stage must be a /var/tmp/gregale-units dir: $stage"
  [[ -z "$only" || "$only" =~ ^[a-z0-9,-]+$ ]] || die "plan services must be daemon names: $only"
  release_dir="/opt/faas/releases/${release_id}"
  while read -r rel source; do
    files+=" $(printf '%q' "${rel}=${stage}/${source}")"
  done < <(jq -r '.replace | to_entries[] | "\(.key) \(.value)"' "$plan")
  while read -r spec; do
    specs+=" $(printf '%q' "$spec")"
  done < <(jq -r '.verify[]' "$plan")
  {
    echo 'set -euo pipefail'
    if [[ -n "$files" ]]; then
      commit="$(plan_field "$plan" .commit)"
      echo "${stage}/bin/deployctl unit-overlay --release-id ${release_id} --expect-current ${expect} --commit ${commit}${files}"
    fi
    echo "${release_dir}/bin/deployctl deploy --only '${only}' --expect-current ${expect} ${release_id}"
    echo "if [[ -n \"\${ROLLOUT_ACTIVATION_MARKER:-}\" ]]; then touch \"\$ROLLOUT_ACTIVATION_MARKER\"; fi"
    # A plan that only applies migrations restarts and verifies no unit.
    if [[ -n "$specs" ]]; then
      echo "if ! bash ${stage}/unit-release-verify.sh ${release_dir}${specs}; then"
      echo "  ${stage}/bin/deployctl rollback --only '${only}' $(rollback_flags "$plan")"
      # Put optional units back on the release now active, too.
      echo "  bash ${stage}/unit-release-verify.sh \"\$(readlink -f /opt/faas/current)\"${specs} || true"
      echo '  exit 1'
      echo 'fi'
    fi
  } > "$out"
}

cmd_run() {
  local script="$1" target
  target="$(deploy_target)"
  remote UNIT "$target" "$(as_root "$target") -s" < "$script"
}

cmd_now() { remote UNIT_GATE "$(gate_target)" 'date +%s'; }

cmd_gate() {
  local plan="$1" deployed_at="$2" kind="$3" target gate_dir args="" duration max_hold
  target="$(gate_target)"
  [[ "$kind" == canary || "$kind" == follower ]] || die "gate kind must be canary or follower: $kind"
  duration="$(jq -er --arg k "$kind" '.gates[$k].duration' "$repo_root/scripts/ci/release_units.json")"
  max_hold="$(jq -er --arg k "$kind" '.gates[$k].max_hold' "$repo_root/scripts/ci/release_units.json")"
  [[ "$deployed_at" =~ ^[0-9]+$ && "$duration" =~ ^[0-9]+[smh]?$ && "$max_hold" =~ ^[0-9]+[smh]?$ ]] \
    || die "bad gate time or durations: $deployed_at $duration $max_hold"
  echo "slo-gate: $kind gate, ${duration} holding alerts for ${max_hold}"
  local prometheus="${PROMETHEUS_URL:-http://127.0.0.1:9095}"
  [[ "$prometheus" =~ ^http://127\.0\.0\.1:[0-9]+$ ]] || die "PROMETHEUS_URL must be a loopback http URL: $prometheus"
  # The remote runner evaluates each unit's argument list: allow only flags
  # and unit/component names.
  jq -e '[.gate[][] | test("^(--)?[a-z0-9-]+$")] | all' "$plan" >/dev/null || {
    echo "::error::slo-gate: plan has an unexpected gate argument" >&2
    return 2
  }
  if [[ "$(jq '.gate | length' "$plan")" -eq 0 ]]; then
    echo "slo-gate: the plan restarts no unit"
    return 0
  fi
  while read -r line; do
    args+=" $(printf '%q' "$line")"
  done < <(jq -r '.gate[] | map(@sh) | join(" ")' "$plan")
  gate_dir="$(remote UNIT_GATE "$target" 'mktemp -d /var/tmp/gregale-slo.XXXXXXXX')"
  [[ "$gate_dir" =~ ^/var/tmp/gregale-slo\.[A-Za-z0-9]+$ ]] || die "unexpected gate dir: $gate_dir"
  copy UNIT_GATE "$repo_root/scripts/ci/slo_gate.py" "$target:$gate_dir/slo_gate.py"
  local status=0
  # One gate per unit, in parallel: each keeps its own scrape check.
  remote UNIT_GATE "$target" "bash -s -- '$gate_dir' '$deployed_at' '$duration' '$max_hold' '$prometheus'$args" <<'REMOTE' || status=$?
set -uo pipefail
dir="$1" deployed_at="$2" duration="$3" max_hold="$4" prometheus="$5"
shift 5
pids=()
for unit_args in "$@"; do
  eval "set -- $unit_args"
  python3 "$dir/slo_gate.py" "$@" --deployed-at "$deployed_at" --duration "$duration" \
    --max-hold "$max_hold" --prometheus-url "$prometheus" &
  pids+=("$!")
done
worst=0
for pid in "${pids[@]}"; do
  wait "$pid"
  code=$?
  case "$code" in
    0) ;;
    1) worst=1 ;;
    *) [[ "$worst" == 1 ]] || worst=3 ;;
  esac
done
rm -rf -- "$dir"
exit "$worst"
REMOTE
  case "$status" in
    0) echo "slo-gate: pass, promote" ;;
    1) echo "::error::slo-gate: an SLO breached after the deploy; rolling back" >&2 ;;
    *) echo "::error::slo-gate: inconclusive (exit $status); no evidence is not a pass, rolling back" >&2; status=3 ;;
  esac
  return "$status"
}

# rollback_flags PLAN: the deployctl rollback flags that put the plan's
# services back on the release that was active before it. A deploy plan names
# that release (expect_current); deployctl then refuses unless the plan's own
# release is still the active one. A converge plan reruns the release already
# active, so deployctl takes the release its activation history records before
# it. Directory times are not a rollback target: after deploy A, roll back,
# deploy B, the newest directory is A, which the first rollback had removed.
rollback_flags() {
  local plan="$1" release_id expect
  release_id="$(plan_field "$plan" .release_id)"
  expect="$(plan_field "$plan" .expect_current)"
  [[ "$release_id" =~ ^[0-9a-f]{40}$ && "$expect" =~ ^[0-9a-f]{40}$ ]] || die "plan release ids must be 40 hex: $release_id, $expect"
  if [[ "$release_id" == "$expect" ]]; then
    printf -- '--expect-current %s' "$release_id"
  else
    printf -- '--expect-current %s --to %s' "$release_id" "$expect"
  fi
}

cmd_rollback() {
  local plan="$1" stage="$2" only specs="" script flags
  only="$(jq -r '.only | join(",")' "$plan")"
  flags="$(rollback_flags "$plan")"
  [[ -z "$only" || "$only" =~ ^[a-z0-9,-]+$ ]] || die "plan services must be daemon names: $only"
  [[ "$stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "stage must be a /var/tmp/gregale-units dir: $stage"
  while read -r spec; do
    specs+=" $(printf '%q' "$spec")"
  done < <(jq -r '.verify[]' "$plan")
  script="$(mktemp "${RUNNER_TEMP:-/tmp}/unit-rollback.XXXXXX")"
  {
    echo 'set -euo pipefail'
    # The staged deployctl: a full release on a compute host ships none.
    echo "${stage}/bin/deployctl rollback --only '${only}' ${flags}"
    [[ -z "$specs" ]] || echo "bash ${stage}/unit-release-verify.sh \"\$(readlink -f /opt/faas/current)\"${specs}"
  } > "$script"
  cmd_run "$script"
  rm -f -- "$script"
}

cmd_cleanup() {
  local stage="$1" target
  target="$(deploy_target)"
  [[ "$stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || return 0
  remote UNIT "$target" "$(as_root "$target") -c 'rm -rf -- $stage'" || true
}

# The host-config record and the ADR-580 contract live here; tests point
# HOST_CONFIG_DIR elsewhere.
host_config_dir() {
  local dir="${HOST_CONFIG_DIR:-/var/lib/faas/bootstrap}"
  [[ "$dir" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "bad host config dir: $dir"
  printf '%s' "$dir"
}

cmd_host_state() {
  local target
  target="$(deploy_target)"
  remote UNIT "$target" "$(as_root "$target") -s -- '$(host_config_dir)'" <<'REMOTE'
set -euo pipefail
current="$(readlink -f /opt/faas/current)"
test -s "$current/release-manifest.json"
record=null
if jq -e 'type == "object"' "$1/control-plane-roles.json" >/dev/null 2>&1; then
  record="$(cat "$1/control-plane-roles.json")"
fi
jq -n --arg current "$(basename "$current")" \
  --argjson release "$(cat "$current/release-manifest.json")" \
  --argjson record "$record" \
  '{current: $current, base_git_sha: $release.git_sha, manifest_hash: $release.manifest_hash, record: $record}'
REMOTE
}

cmd_host_manifest() {
  local state="$1" out="$2" target want got
  target="$(deploy_target)"
  want="$(plan_field "$state" .manifest_hash)"
  [[ "$want" =~ ^sha256:[0-9a-f]{64}$ ]] || die "host state has no manifest_hash: $want"
  # The command runs on the host, which resolves its own active release.
  # shellcheck disable=SC2016
  remote UNIT "$target" 'cat "$(readlink -f /opt/faas/current)/production-manifest.yaml"' > "$out"
  got="sha256:$(sha256sum "$out" | awk '{print $1}')"
  [[ "$got" == "$want" ]] || die "the active release's production manifest is $got, its release manifest says $want"
}

cmd_host_stage() {
  local deployctl="$1" target remote_stage
  target="$(deploy_target)"
  remote_stage="$(remote UNIT "$target" 'mktemp -d /var/tmp/gregale-units.XXXXXXXX')"
  [[ "$remote_stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "unexpected remote stage dir: $remote_stage"
  remote UNIT "$target" "mkdir -p '$remote_stage/bin'"
  copy UNIT "$deployctl" "$target:$remote_stage/bin/deployctl"
  remote UNIT "$target" "chmod 0755 '$remote_stage/bin/deployctl'"
  printf '%s\n' "$remote_stage"
}

cmd_host_restart_script() {
  local stage="$1" out="$2"
  [[ "$stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "stage must be a /var/tmp/gregale-units dir: $stage"
  {
    echo 'set -euo pipefail'
    echo "${stage}/bin/deployctl restart-changed --json > ${stage}/restart.json"
    echo "if [[ -n \"\${ROLLOUT_ACTIVATION_MARKER:-}\" ]]; then touch \"\$ROLLOUT_ACTIVATION_MARKER\"; fi"
  } > "$out"
}

cmd_host_restarted() {
  local stage="$1" target
  target="$(deploy_target)"
  [[ "$stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "stage must be a /var/tmp/gregale-units dir: $stage"
  remote UNIT "$target" "$(as_root "$target") -c 'cat $stage/restart.json'"
}

cmd_host_record() {
  local record="$1" contract="$2" target payload
  target="$(deploy_target)"
  jq -e '.kind == "gregale-host-config" and .group == "control_plane"
         and (.commit | test("^[0-9a-f]{40}$")) and (.environment | test("^sha256:[0-9a-f]{64}$"))
         and (.roles | type == "object")' "$record" >/dev/null || die "not a host-config record with an environment: $record"
  [[ "$contract" =~ ^sha256:[0-9a-f]{64}$ ]] || die "bad bootstrap contract: $contract"
  payload="$(base64 < "$record" | tr -d '\n')"
  [[ "$payload" =~ ^[A-Za-z0-9+/=]+$ ]] || die "record did not encode"
  remote UNIT "$target" "$(as_root "$target") -s -- '$(host_config_dir)' '$payload' '$contract'" <<'REMOTE'
set -euo pipefail
dir="$1"
install -d -m 0700 "$dir"
umask 077
printf '%s' "$2" | base64 --decode > "$dir/.control-plane-roles.json.new"
jq -e '.kind == "gregale-host-config"' "$dir/.control-plane-roles.json.new" >/dev/null
printf '%s\n' "$3" > "$dir/.control-plane-bootstrap-contract.sha256.new"
mv -f "$dir/.control-plane-roles.json.new" "$dir/control-plane-roles.json"
mv -f "$dir/.control-plane-bootstrap-contract.sha256.new" "$dir/control-plane-bootstrap-contract.sha256"
REMOTE
}

command="${1:-}"
shift || true
case "$command" in
  state) cmd_state "$@" ;;
  stage) cmd_stage "$@" ;;
  script) cmd_script "$@" ;;
  run) cmd_run "$@" ;;
  now) cmd_now "$@" ;;
  gate) cmd_gate "$@" ;;
  rollback) cmd_rollback "$@" ;;
  cleanup) cmd_cleanup "$@" ;;
  host-state) cmd_host_state "$@" ;;
  host-manifest) cmd_host_manifest "$@" ;;
  host-stage) cmd_host_stage "$@" ;;
  host-restart-script) cmd_host_restart_script "$@" ;;
  host-restarted) cmd_host_restarted "$@" ;;
  host-record) cmd_host_record "$@" ;;
  *) echo "usage: $0 state|stage|script|run|now|gate|rollback|cleanup|host-state|host-manifest|host-stage|host-restart-script|host-restarted|host-record ..." >&2; exit 2 ;;
esac
