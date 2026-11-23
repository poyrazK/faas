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
#   unit_deploy.sh record NODE                   import the node's spooled deploys and rollbacks
#                                                into the release history on the control plane
#   unit_deploy.sh preflight-state               print the control plane's deploy-lock state, the
#                                                alerts its Prometheus has active and the watch
#                                                record, as JSON, for scripts/ci/deploy_preflight.py
#
# The post-deploy watch (docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md),
# with its record on the control plane (WATCH_RECORD, default
# /var/lib/faas/deploy/unit-watch.json) and the run that owns it
# (WATCH_OWNER, default GITHUB_RUN_ID):
#
#   unit_deploy.sh watch-open PLAN HOST [TARGET] before activating HOST: add the plan's release to the
#                                                record and take it over (exit 3: HOST holds the most
#                                                unwatched releases allowed)
#   unit_deploy.sh watch-revert HOST             after a failed deploy of HOST: record what it runs now
#   unit_deploy.sh watch-record                  print the record (null when none)
#   unit_deploy.sh watch                         run the SLO gate in watch mode over the record's units
#                                                (0 pass, 1 breach, 3 inconclusive, 4 superseded)
#   unit_deploy.sh watch-close                   remove the record if this run owns it (exit 4 if not)
#   unit_deploy.sh watch-adopt                   take over a record no watch runs for, a window from now
#   unit_deploy.sh watch-host-config PREV COMMIT note a host-config step in the record (breach converges back)
#   unit_deploy.sh watch-stage DEPLOYCTL         stage deployctl and the unit verifier on the deploy host
#   unit_deploy.sh watch-rollback ENTRY STAGE    put the deploy host back on its record entry's
#                                                known-good release (exit 5: the host changed since)
#   unit_deploy.sh pin-node HOST PORT FP OUT     known_hosts for a node, only keys with fingerprint FP
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
  # In a CI job, one connection per host carries every ssh and scp of the
  # job: a new connection from the busy control-plane runner costs about a
  # second, and a node deploy makes a dozen. UNIT_SSH_MUX_DIR= turns it off.
  # ssh refuses a socket path that does not fit a sockaddr_un (104 bytes on
  # macOS) while it creates it as <dir>/<40-hex %C>.<16 random>, so a longer
  # directory connects without sharing.
  local mux="${UNIT_SSH_MUX_DIR-${RUNNER_TEMP:+$RUNNER_TEMP/ssh-mux}}"
  if [[ -n "$mux" ]] && (( ${#mux} + 58 < 104 )); then
    install -d -m 0700 "$mux"
    printf '%s\n' -o ControlMaster=auto -o "ControlPath=$mux/%C" -o ControlPersist=120
  fi
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

# actor_line prints the export that names this workflow run as the actor in
# the release history deployctl writes (issue #574), when there is a run.
actor_line() {
  [[ "${GITHUB_RUN_ID:-}" =~ ^[0-9]+$ ]] || return 0
  printf 'export FAAS_DEPLOY_ACTOR=%q\n' "${GITHUB_WORKFLOW:-cd} run ${GITHUB_RUN_ID}"
}

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
  # Not compressed: gzip of the binaries cost seconds of the busy runner's
  # CPU and the hosts share a fast network.
  tar --create --file "$local_stage.tar" --directory "$local_stage" .
  remote_stage="$(remote UNIT "$target" 'mktemp -d /var/tmp/gregale-units.XXXXXXXX')"
  [[ "$remote_stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "unexpected remote stage dir: $remote_stage"
  copy UNIT "$local_stage.tar" "$target:$remote_stage/stage.tar"
  rm -f -- "$local_stage.tar"
  remote UNIT "$target" "tar --extract --file '$remote_stage/stage.tar' --directory '$remote_stage' --no-same-owner && rm -f -- '$remote_stage/stage.tar'"
  printf '%s\n' "$remote_stage"
}

# The activation script runs on the host as root. It assembles the overlay
# (deploy), activates it restarting only the plan's services, and gates each
# unit's processes and readiness; a failed unit gate rolls straight back.
# deployctl deploy applies the overlay's migrations first, so a failed
# migration stops the script before any restart.
cmd_script() {
  local plan="$1" stage="$2" out="$3" release_id expect only commit release_dir files="" specs="" parallel
  release_id="$(plan_field "$plan" .release_id)"
  # Parallel activation (cmd/deployctl/restart_waves.go). An environment
  # variable: a deployctl from before it ignores it and restarts one at a
  # time.
  parallel="$(jq -er '.activation.parallel' "$repo_root/scripts/ci/release_units.json")"
  [[ "$parallel" =~ ^([1-9]|1[0-6])$ ]] || die "release_units.json activation.parallel must be 1..16: $parallel"
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
  local adopts=false
  if jq -e '.units | index("vmmd")' "$plan" >/dev/null; then
    adopts=true
  fi
  {
    echo 'set -euo pipefail'
    actor_line
    # vmmd restarts only behind its adoption proof, checked before anything
    # changes (docs/adr/20261123122457-compute-unit-lane.md, decision 3).
    if [[ "$adopts" == true ]]; then
      adoption_check_lines "$stage"
    fi
    if [[ -n "$files" ]]; then
      commit="$(plan_field "$plan" .commit)"
      echo "${stage}/bin/deployctl unit-overlay --release-id ${release_id} --expect-current ${expect} --commit ${commit}${files}"
    fi
    echo "DEPLOYCTL_RESTART_PARALLEL=${parallel} ${release_dir}/bin/deployctl deploy --only '${only}' --expect-current ${expect} ${release_id}"
    echo "if [[ -n \"\${ROLLOUT_ACTIVATION_MARKER:-}\" ]]; then touch \"\$ROLLOUT_ACTIVATION_MARKER\"; fi"
    if [[ "$adopts" == true ]]; then
      # A refusal, a fenced claim or no report in time: schedd already
      # fails the guests the new vmmd does not hold, so roll back now.
      echo "if ! /opt/faas/current/bin/vmmd adoption-report --wait 90s --already-gone \"\$adoption_gone\"; then"
      echo "  echo 'unit-lane: the restarted vmmd did not report a clean adoption; rolling the node back' >&2"
      echo "  ${stage}/bin/deployctl rollback --only '${only}' $(rollback_flags "$plan")"
      echo '  exit 1'
      echo 'fi'
    fi
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

# The no-drain proof node_join runs around a full release's vmmd restart
# (docs/adr/20261017-vmmd-restart-adoption.md), emitted for a unit release
# that restarts vmmd. The staged candidate judges the serving vmmd's journal
# and live guests with the adoption switches the serving process runs with
# (a unit release changes no compute role, so the restart keeps them).
# Retries while only guests are refused (ADR 20261116191802). Anything but a
# full adoption stands down before the overlay exists: exit 3, nothing on
# the node changed, no guest parked or drained (owner decision 2026-11-23).
adoption_check_lines() {
  local stage="$1"
  cat <<EOF
stand_down() { echo "unit-lane: standing down on this node, nothing changed: \$*" >&2; exit 3; }
vmmd_pid="\$(systemctl show faas-vmmd -p MainPID --value)"
[[ "\${vmmd_pid:-0}" -gt 0 ]] || stand_down "vmmd is not running"
adopt_env=()
while IFS= read -r kv; do adopt_env+=("\$kv"); done < <(tr '\\0' '\\n' < "/proc/\$vmmd_pid/environ" |
  grep -E '^FAAS_VMMD_(ADOPTION_RECORD|ADOPT_RUNNING_VMS|ADOPT_ADMITTED_RUNTIMES|FLAT_TENANT_CGROUPS)=' || true)
printf '%s\\n' "\${adopt_env[@]}" | grep -Eqx 'FAAS_VMMD_ADOPT_RUNNING_VMS=(1|t|T|true|TRUE|True)' ||
  stand_down "the serving vmmd does not hand its guests off (adopt_running_vms is off)"
adoption_rc=3
for attempt in 1 2 3 4 5 6 7; do
  adoption_rc=0
  adoption="\$(env "\${adopt_env[@]}" ${stage}/bin/vmmd adoption-check --config /etc/faas/vmmd.toml)" || adoption_rc=\$?
  [[ "\$adoption_rc" -eq 3 ]] || break
  # A node-level refusal names a top-level reason; waiting does not help.
  if printf '%s' "\$adoption" | jq -e '(.reason // "") != ""' >/dev/null 2>&1; then break; fi
  echo "unit-lane: adoption-check attempt \$attempt/7 refused guests; retrying in 10s" >&2
  [[ "\$attempt" -lt 7 ]] && sleep 10
done
printf '%s\\n' "\$adoption"
[[ "\$adoption_rc" -eq 0 ]] || stand_down "the candidate vmmd does not adopt every live guest (adoption-check exit \$adoption_rc)"
adoption_gone="\$(printf '%s' "\$adoption" | jq -r '[.gone[]?.instance | select(type == "string")] | join(",")')"
EOF
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
    actor_line
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

# Issue #574: a compute host has no owner access to the release history, so
# its deployctl spools each finished deploy and rollback
# (cmd/deployctl/history.go, historySpoolPath). This copies the spool to the
# control plane, where `deployctl record` imports it as the schema owner
# under the node's compute_nodes name: <node>.faas for a compute-only box,
# as `gregalectl release install` stamps it (canonicalComputeNodeName). Each
# line keeps the id it got on the node, so importing the spool again adds
# nothing. Best effort for its callers: the history never stops a lane.
cmd_record() {
  local node="$1" host target gate spool
  local spool_path="${ACTIVATION_SPOOL:-/var/lib/faas/deploy/activations.jsonl}"
  local deployctl="${CP_DEPLOYCTL:-/opt/faas/current/bin/deployctl}"
  [[ "$node" =~ ^[a-z0-9][a-z0-9.-]{0,240}$ ]] || die "record needs a node name: $node"
  [[ "$spool_path" =~ ^/[A-Za-z0-9._/-]+$ && "$deployctl" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "bad spool or deployctl path"
  host="$node"
  [[ "$host" == *.faas ]] || host="${host}.faas"
  target="$(deploy_target)"
  spool="$(remote UNIT "$target" "$(as_root "$target") -c 'cat -- $spool_path 2>/dev/null || true'")"
  if [[ -z "$spool" ]]; then
    echo "release history: $node spooled no activations"
    return 0
  fi
  gate="$(gate_target)"
  printf '%s\n' "$spool" | remote UNIT_GATE "$gate" "$(as_root "$gate") -c '$deployctl record --host $host'"
}

# The deploy admission's view of the control plane
# (docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md): whether
# deployctl's lock is free, and the alerts Prometheus has active. One SSH
# round-trip; the remote side never fails, it reports what it could not read.
cmd_preflight_state() {
  local target prometheus="${PROMETHEUS_URL:-http://127.0.0.1:9095}"
  local lock="${DEPLOY_LOCK_PATH:-/run/lock/faas-deploy.lock}"
  target="$(gate_target)"
  [[ "$prometheus" =~ ^http://127\.0\.0\.1:[0-9]+$ ]] || die "PROMETHEUS_URL must be a loopback http URL: $prometheus"
  [[ "$lock" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "bad deploy lock path: $lock"
  remote UNIT_GATE "$target" "$(as_root "$target") -s -- '$prometheus' '$lock' '$(watch_record_path)'" <<'REMOTE'
set -uo pipefail
prometheus="$1" lock="$2" record="$3"
# flock -n exits 75 (-E) when deployctl holds the lock.
lock_state=free
flock -n -E 75 "$lock" true
case "$?" in
  0) ;;
  75) lock_state=held ;;
  *) lock_state=unknown ;;
esac
# The post-deploy watch's record: its owner and unwatched releases per host.
watch=null
if [[ -s "$record" ]] && jq -e '.kind == "gregale-unit-watch"' "$record" >/dev/null 2>&1; then
  watch="$(jq -c '{owner_run, hosts: (.hosts | map_values({releases, known_good, current}))}' "$record")"
fi
# The full release the control plane runs: the planner refuses a unit release
# built over another one, so the preflight stands down on it first.
base="$(jq -r '.git_sha // empty' "$(readlink -f /opt/faas/current)/release-manifest.json" 2>/dev/null)"
err="$(mktemp)" body="$(mktemp)"
# The alert list can outgrow one command-line argument: jq reads it from a file.
if curl -fsS --max-time 10 -o "$body" "$prometheus/api/v1/alerts" 2>"$err" \
    && jq -ce --arg lock "$lock_state" --argjson watch "$watch" --arg base "$base" 'select(.status == "success" and (.data.alerts | type) == "array")
        | {lock: $lock, watch: $watch, base: $base, prometheus: {ok: true, alerts: .data.alerts}}' "$body" 2>>"$err"; then
  :
else
  jq -cn --arg lock "$lock_state" --argjson watch "$watch" --arg base "$base" --arg error "$(tr '\n' ' ' < "$err" | cut -c1-300)" \
    '{lock: $lock, watch: $watch, base: $base, prometheus: {ok: false, error: (if $error == "" then "no alert list in the answer" else $error end)}}'
fi
rm -f -- "$err" "$body"
REMOTE
}

# The post-deploy watch (docs/adr/20261020214632-automatic-unit-deploy-safety-
# rails.md, section 3). The record of unit releases not yet watched clean
# lives on the control plane (the gate target) whichever host a deploy
# changes; scripts/ci/unit_watch_record.py keeps it.
watch_record_path() {
  local path="${WATCH_RECORD:-/var/lib/faas/deploy/unit-watch.json}"
  [[ "$path" =~ ^/[A-Za-z0-9._/-]+\.json$ ]] || die "bad watch record path: $path"
  printf '%s' "$path"
}

watch_owner() {
  local owner="${WATCH_OWNER:-${GITHUB_RUN_ID:-}}"
  [[ "$owner" =~ ^[0-9]+$ ]] || die "the watch owner (WATCH_OWNER or GITHUB_RUN_ID) must be a run id: '$owner'"
  printf '%s' "$owner"
}

watch_config() { jq -er --arg k "$1" '.watch[$k]' "$repo_root/scripts/ci/release_units.json"; }

# watch_record_run COMMAND [FILE...] -- ARGS...: run unit_watch_record.py
# COMMAND on the control plane as root with ARGS; each FILE is copied next to
# it first and named in ARGS by its basename as @NAME.
watch_record_run() {
  local command="$1" target dir args="" arg status=0
  shift
  local -a files=()
  while [[ $# -gt 0 && "$1" != -- ]]; do files+=("$1"); shift; done
  [[ "${1:-}" == -- ]] && shift
  target="$(gate_target)"
  dir="$(remote UNIT_GATE "$target" 'mktemp -d /var/tmp/gregale-watch.XXXXXXXX')"
  [[ "$dir" =~ ^/var/tmp/gregale-watch\.[A-Za-z0-9]+$ ]] || die "unexpected watch dir: $dir"
  copy UNIT_GATE "$repo_root/scripts/ci/unit_watch_record.py" ${files[@]+"${files[@]}"} "$target:$dir/"
  for arg in "$@"; do
    [[ "$arg" == @* ]] && arg="$dir/${arg#@}"
    args+=" $(printf '%q' "$arg")"
  done
  remote UNIT_GATE "$target" "$(as_root "$target") -c 'python3 $dir/unit_watch_record.py $command --record $(watch_record_path)$args; status=\$?; rm -rf -- $dir; exit \$status'" || status=$?
  return "$status"
}

# watch-open PLAN HOST [TARGET_JSON_FILE]: before a unit deploy activates
# HOST, add the plan's release to the record and make this run its owner.
# Exits 3 when HOST already holds the most unwatched releases allowed.
cmd_watch_open() {
  local plan="$1" host="$2" target_file="${3:-}" state live base commit="${UNITS_SHA:-}" now
  [[ "$host" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || die "bad host label: $host"
  [[ -z "$commit" || "$commit" =~ ^[0-9a-f]{40}$ ]] || die "UNITS_SHA must be 40 hex: $commit"
  state="$(cmd_state)"
  live="$(jq -er .current <<<"$state")"
  base="$(jq -er .base_git_sha <<<"$state")"
  now="$(cmd_now)"
  local -a files=("$plan") args=(--owner "$(watch_owner)" --host "$host" --plan "@$(basename "$plan")"
    --live "$live" --base "$base" --now "$now" --max-unwatched "$(watch_config max_unwatched)")
  [[ -z "$commit" ]] || args+=(--commit "$commit")
  if [[ -n "$target_file" ]]; then
    files+=("$target_file")
    args+=(--target-file "@$(basename "$target_file")")
  fi
  watch_record_run open "${files[@]}" -- "${args[@]}" >/dev/null
}

# watch-revert HOST: a deploy of HOST failed and rolled back; record the
# release HOST is on now.
cmd_watch_revert() {
  local host="$1" live
  [[ "$host" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || die "bad host label: $host"
  live="$(cmd_state | jq -er .current)"
  watch_record_run revert -- --host "$host" --live "$live" >/dev/null
}

cmd_watch_record() { watch_record_run show; }

cmd_watch_close() { watch_record_run close -- --owner "$(watch_owner)"; }

# watch-adopt: make this run the watch of a record no watch runs for; it
# then watches a full window from now.
cmd_watch_adopt() { watch_record_run adopt -- --owner "$(watch_owner)" --now "$(cmd_now)"; }

# watch-host-config PREVIOUS COMMIT: before a host-config step converges
# COMMIT over PREVIOUS (the control plane's role record), note it in the
# watch record, so a breach converges the roles back
# (docs/adr/20261122170000-continuous-unit-lane.md, "Config rollback").
cmd_watch_host_config() {
  [[ "${1:-}" =~ ^[0-9a-f]{40}$ && "${2:-}" =~ ^[0-9a-f]{40}$ ]] || die "watch-host-config needs two 40-hex commits"
  watch_record_run host-config -- --owner "$(watch_owner)" --previous "$1" --commit "$2" --now "$(cmd_now)"
}

# watch: run the SLO gate in watch mode on the control plane for every unit
# the record holds, from the first unwatched activation until `window` after
# the last. Exits 0 (pass), 1 (breach), 3 (inconclusive) or 4 (another run
# owns the record, or it is closed).
cmd_watch() {
  local target gate_dir owner prometheus="${PROMETHEUS_URL:-http://127.0.0.1:9095}" window interval blind
  target="$(gate_target)"
  owner="$(watch_owner)"
  window="$(watch_config window)"
  interval="$(watch_config interval)"
  blind="$(watch_config max_blind_rounds)"
  [[ "$prometheus" =~ ^http://127\.0\.0\.1:[0-9]+$ ]] || die "PROMETHEUS_URL must be a loopback http URL: $prometheus"
  [[ "$window" =~ ^[0-9]+m$ && "$interval" =~ ^[0-9]+s$ && "$blind" =~ ^[0-9]+$ ]] || die "bad watch config: $window $interval $blind"
  gate_dir="$(remote UNIT_GATE "$target" 'mktemp -d /var/tmp/gregale-slo.XXXXXXXX')"
  [[ "$gate_dir" =~ ^/var/tmp/gregale-slo\.[A-Za-z0-9]+$ ]] || die "unexpected gate dir: $gate_dir"
  copy UNIT_GATE "$repo_root/scripts/ci/slo_gate.py" "$target:$gate_dir/slo_gate.py"
  local status=0
  remote UNIT_GATE "$target" "$(as_root "$target") -s -- '$gate_dir' '$(watch_record_path)' '$owner' '$prometheus' '${window%m}' '${interval%s}' '$blind'" <<'REMOTE' || status=$?
set -uo pipefail
dir="$1" record="$2" owner="$3" prometheus="$4" window=$(( $5 * 60 )) interval="$6" blind="$7"
trap 'rm -rf -- "$dir"' EXIT
if ! jq -e --arg o "$owner" '.owner_run == $o' "$record" >/dev/null 2>&1; then
  echo "unit-watch: run $owner does not own the watch record ($(jq -r '.owner_run // "closed"' "$record" 2>/dev/null || echo closed))"
  exit 4
fi
# Gate arguments are flags and unit/component names only: the record is
# written from plans, and this runner evaluates them.
# The control plane's host configuration (.host_config) is watched beside
# the hosts' units, with the platform SLOs (ADR-20261122170000).
jq -e '[(.hosts[].gate[][]), ((.host_config.gate // [])[][]) | test("^(--)?[a-z0-9-]+$")] | all' "$record" >/dev/null || { echo "unit-watch: unexpected gate argument in the record"; exit 2; }
deployed_at="$(jq '[(.hosts[].first_activation), (.host_config.first_activation // empty)] | min' "$record")"
end=$(( $(jq '[([(.hosts[].last_activation), (.host_config.last_activation // empty)] | max), (.watch_from // 0)] | max' "$record") + window ))
duration=$(( end - $(date +%s) ))
(( duration >= interval )) || duration="$interval"
echo "unit-watch: watching $(jq -r '[(.hosts | keys[]), (if .host_config then "host configuration" else empty end)] | join(", ")' "$record") for ${duration}s (since the first unwatched activation at $deployed_at)"
pids=()
while read -r unit_args; do
  eval "set -- $unit_args"
  python3 "$dir/slo_gate.py" "$@" --deployed-at "$deployed_at" --duration "${duration}s" --max-hold "${window}s" \
    --interval "${interval}s" --max-blind-rounds "$blind" --prometheus-url "$prometheus" \
    --watch-record "$record" --owner "$owner" &
  pids+=("$!")
done < <(jq -r '[(.hosts[].gate[]), ((.host_config.gate // [])[])] | unique[] | map(@sh) | join(" ")' "$record")
worst=0
for pid in "${pids[@]}"; do
  wait "$pid"
  case "$?" in
    0) ;;
    1) worst=1 ;;
    3) [[ "$worst" == 1 ]] || worst=3 ;;
    4) [[ "$worst" == 1 || "$worst" == 3 ]] || worst=4 ;;
    *) [[ "$worst" == 1 ]] || worst=3 ;;
  esac
done
exit "$worst"
REMOTE
  return "$status"
}

# watch-stage DEPLOYCTL: copy deployctl and the unit verifier to the deploy
# host for a watch rollback; print the stage dir.
cmd_watch_stage() {
  local deployctl="$1" target remote_stage
  target="$(deploy_target)"
  remote_stage="$(remote UNIT "$target" 'mktemp -d /var/tmp/gregale-units.XXXXXXXX')"
  [[ "$remote_stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "unexpected remote stage dir: $remote_stage"
  remote UNIT "$target" "mkdir -p '$remote_stage/bin'"
  copy UNIT "$deployctl" "$target:$remote_stage/bin/deployctl"
  copy UNIT "$repo_root/scripts/unit-release-verify.sh" "$target:$remote_stage/unit-release-verify.sh"
  remote UNIT "$target" "chmod 0755 '$remote_stage/bin/deployctl'"
  printf '%s\n' "$remote_stage"
}

# watch-rollback ENTRY_JSON STAGE: put the deploy host back on the record
# entry's known-good release. Exits 0 when it rolled back or was already
# there, 5 when the host runs a release the record does not expect (it was
# changed by something else and is left alone).
cmd_watch_rollback() {
  local entry="$1" stage="$2" known current only specs="" script status=0
  known="$(jq -er .known_good "$entry")"
  current="$(jq -er .current "$entry")"
  only="$(jq -r '.services | join(",")' "$entry")"
  [[ "$known" =~ ^[0-9a-f]{40}$ && "$current" =~ ^[0-9a-f]{40}$ ]] || die "record releases must be 40 hex: $known, $current"
  [[ "$only" =~ ^[a-z0-9,-]+$ ]] || die "record services must be daemon names: $only"
  [[ "$stage" =~ ^/var/tmp/gregale-units\.[A-Za-z0-9]+$ ]] || die "stage must be a /var/tmp/gregale-units dir: $stage"
  while read -r spec; do
    specs+=" $(printf '%q' "$spec")"
  done < <(jq -r '.verify[]' "$entry")
  script="$(mktemp "${RUNNER_TEMP:-/tmp}/unit-watch-rollback.XXXXXX")"
  {
    echo 'set -euo pipefail'
    actor_line
    # Evaluated on the host.
    # shellcheck disable=SC2016
    echo 'live="$(basename "$(readlink -f /opt/faas/current)")"'
    echo "if [[ \"\$live\" == $known ]]; then echo \"already on the known-good release $known\"; exit 0; fi"
    echo "if [[ \"\$live\" != $current ]]; then echo \"runs \$live, not $current: changed since the watch record; left alone\" >&2; exit 5; fi"
    echo "${stage}/bin/deployctl rollback --only '${only}' --expect-current ${current} --to ${known}"
    [[ -z "$specs" ]] || echo "bash ${stage}/unit-release-verify.sh \"\$(readlink -f /opt/faas/current)\"${specs}"
  } > "$script"
  cmd_run "$script" || status=$?
  rm -f -- "$script"
  return "$status"
}

# pin-node HOST PORT FINGERPRINT OUT: write OUT as a known_hosts file holding
# only the keys HOST presents whose SHA256 fingerprint is FINGERPRINT.
cmd_pin_node() {
  local host="$1" port="$2" fingerprint="$3" out="$4" scanned line got
  [[ "$host" =~ ^[A-Za-z0-9.:-]+$ && "$port" =~ ^[0-9]+$ && "$fingerprint" =~ ^SHA256:[A-Za-z0-9+/]{43}$ ]] \
    || die "bad node address or fingerprint: $host $port $fingerprint"
  : > "$out"
  scanned="$(ssh-keyscan -T 10 -t ed25519,ecdsa,rsa -p "$port" "$host" 2>/dev/null || true)"
  while IFS= read -r line; do
    [[ -n "$line" && "$line" != \#* ]] || continue
    got="$(ssh-keygen -lf - -E sha256 <<<"$line" 2>/dev/null | awk 'NR == 1 { print $2 }')"
    [[ "$got" != "$fingerprint" ]] || printf '%s\n' "$line" >> "$out"
  done <<<"$scanned"
  [[ -s "$out" ]] || die "$host presented no host key with fingerprint $fingerprint"
}

# The host-config record and the ADR-580 contract live here; tests point
# HOST_CONFIG_DIR elsewhere.
host_config_dir() {
  local dir="${HOST_CONFIG_DIR:-/var/lib/faas/bootstrap}"
  [[ "$dir" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "bad host config dir: $dir"
  printf '%s' "$dir"
}

# The active release link; tests point HOST_CURRENT_RELEASE elsewhere.
host_current_release() {
  local link="${HOST_CURRENT_RELEASE:-/opt/faas/current}"
  [[ "$link" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "bad current release link: $link"
  printf '%s' "$link"
}

# host-state prints what the control plane runs and last converged. A host
# with no active release at all (no /opt/faas/current: a control plane
# rebuilt from backups before its first activation) reports current,
# base_git_sha and manifest_hash as null, so a full release converges every
# role (plan_host_config.py) and a unit release is refused. A link whose
# release has no release-manifest.json is a broken host and still fails.
cmd_host_state() {
  local target dir link
  target="$(deploy_target)"
  # Resolved here, not inside the remote command's quotes, so a refused path
  # stops the command instead of passing an empty argument.
  dir="$(host_config_dir)"
  link="$(host_current_release)"
  remote UNIT "$target" "$(as_root "$target") -s -- '$dir' '$link'" <<'REMOTE'
set -euo pipefail
record=null
if jq -e 'type == "object"' "$1/control-plane-roles.json" >/dev/null 2>&1; then
  record="$(cat "$1/control-plane-roles.json")"
fi
contract=""
if [[ -f "$1/control-plane-bootstrap-contract.sha256" ]]; then
  contract="$(tr -d '[:space:]' < "$1/control-plane-bootstrap-contract.sha256")"
fi
if [[ ! -e "$2" && ! -L "$2" ]]; then
  jq -n --argjson record "$record" --arg contract "$contract" \
    '{current: null, base_git_sha: null, manifest_hash: null, record: $record,
      contract: (if $contract == "" then null else $contract end)}'
  exit 0
fi
current="$(readlink -f "$2")"
test -s "$current/release-manifest.json"
jq -n --arg current "$(basename "$current")" \
  --argjson release "$(cat "$current/release-manifest.json")" \
  --argjson record "$record" --arg contract "$contract" \
  '{current: $current, base_git_sha: $release.git_sha, manifest_hash: $release.manifest_hash, record: $record,
    contract: (if $contract == "" then null else $contract end)}'
REMOTE
}

# rollback-readiness: read-only facts a rollback depends on, as JSON, for
# scripts/ci/rollback_drill.py (docs/adr/20261122170000-continuous-unit-
# lane.md, "Rollback drill"): the active release, the activation history's
# last two entries (deployctl rollback restores the one before the active),
# whether that predecessor and the watch record's known-good control-plane
# release are still installed with a manifest and binaries, and whether the
# host-config record exists. Changes nothing.
cmd_rollback_readiness() {
  local target dir link record
  target="$(deploy_target)"
  dir="$(host_config_dir)"
  link="$(host_current_release)"
  record="$(watch_record_path)"
  remote UNIT "$target" "$(as_root "$target") -s -- '$dir' '$link' '$record'" <<'REMOTE'
set -uo pipefail
dir="$1" link="$2" record="$3"
releases="$(dirname "$(readlink -f "$link" 2>/dev/null || echo /opt/faas/releases/none)")"
installed() { # installed RELEASE_ID -> true when it has a manifest and binaries
  [[ -n "$1" && -s "$releases/$1/release-manifest.json" && -d "$releases/$1/bin" ]] \
    && jq -e 'type == "object"' "$releases/$1/release-manifest.json" >/dev/null 2>&1
}
current=""
[[ -e "$link" || -L "$link" ]] && current="$(basename "$(readlink -f "$link")")"
history="$releases/.activation-history.json"
last="" prev=""
if jq -e '.releases | type == "array"' "$history" >/dev/null 2>&1; then
  last="$(jq -r '.releases[-1] // ""' "$history")"
  prev="$(jq -r '.releases[-2] // ""' "$history")"
fi
known=""
if jq -e '.kind == "gregale-unit-watch"' "$record" >/dev/null 2>&1; then
  known="$(jq -r '.hosts["control-plane"].known_good // ""' "$record")"
fi
hc=false
jq -e '.kind == "gregale-host-config" and (.commit | test("^[0-9a-f]{40}$"))' "$dir/control-plane-roles.json" >/dev/null 2>&1 && hc=true
retained="$(find "$releases" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | wc -l | tr -d ' ')"
jq -n --arg current "$current" --arg last "$last" --arg prev "$prev" --arg known "$known" \
  --argjson prev_ok "$(installed "$prev" && echo true || echo false)" \
  --argjson known_ok "$( [[ -z "$known" ]] && echo null || { installed "$known" && echo true || echo false; })" \
  --argjson host_config "$hc" --argjson retained "${retained:-0}" \
  '{current: $current, history_last: $last, predecessor: $prev, predecessor_installed: $prev_ok,
    watch_known_good: (if $known == "" then null else $known end), watch_known_good_installed: $known_ok,
    host_config_record: $host_config, retained: $retained}'
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
  # The record names the contract written beside it: a convergence that
  # moves the contract without the record (an older release's whole play)
  # makes the record stale, and the planners then converge every role
  # (docs/adr/20261030150000-full-release-converges-changed-roles.md).
  payload="$(jq -c --arg contract "$contract" '.contract = $contract' "$record" | base64 | tr -d '\n')"
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
  record) cmd_record "$@" ;;
  host-state) cmd_host_state "$@" ;;
  host-manifest) cmd_host_manifest "$@" ;;
  host-stage) cmd_host_stage "$@" ;;
  host-restart-script) cmd_host_restart_script "$@" ;;
  host-restarted) cmd_host_restarted "$@" ;;
  host-record) cmd_host_record "$@" ;;
  rollback-readiness) cmd_rollback_readiness "$@" ;;
  preflight-state) cmd_preflight_state "$@" ;;
  watch-open) cmd_watch_open "$@" ;;
  watch-revert) cmd_watch_revert "$@" ;;
  watch-record) cmd_watch_record "$@" ;;
  watch) cmd_watch "$@" ;;
  watch-close) cmd_watch_close "$@" ;;
  watch-adopt) cmd_watch_adopt "$@" ;;
  watch-host-config) cmd_watch_host_config "$@" ;;
  watch-stage) cmd_watch_stage "$@" ;;
  watch-rollback) cmd_watch_rollback "$@" ;;
  pin-node) cmd_pin_node "$@" ;;
  *) echo "usage: $0 state|stage|script|run|now|gate|rollback|cleanup|host-state|host-manifest|host-stage|host-restart-script|host-restarted|host-record|rollback-readiness|preflight-state|watch-open|watch-revert|watch-record|watch|watch-close|watch-adopt|watch-host-config|watch-stage|watch-rollback|pin-node ..." >&2; exit 2 ;;
esac
