#!/usr/bin/env bash
# Hermetic tests for unit_deploy.sh. ssh and scp are stubbed to run on this
# machine, and python3 inside the "remote" gate runner is stubbed per unit,
# so no host, systemd or Prometheus is needed.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
helper="$repo_root/scripts/ci/unit_deploy.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
stub="$test_root/stub"
mkdir -p "$stub"
real_python="$(command -v python3)"

# ssh [opts] TARGET COMMAND...: run COMMAND here, logging what ran as root.
cat > "$stub/ssh" <<'EOF'
#!/usr/bin/env bash
while [[ "$1" == -* ]]; do
  case "$1" in -i|-p|-o) shift 2 ;; *) shift ;; esac
done
target="$1"
shift
printf '%s\t%s\n' "$target" "$*" >> "$STUB_LOG"
exec bash -c "$*"
EOF
# scp [opts] SRC... TARGET:DEST
cat > "$stub/scp" <<'EOF'
#!/usr/bin/env bash
args=()
while [[ $# -gt 0 ]]; do
  case "$1" in -i|-P|-o) shift 2 ;; -q) shift ;; *) args+=("$1"); shift ;; esac
done
last=$(( ${#args[@]} - 1 ))
dest="${args[$last]#*:}"
unset "args[$last]"
cp "${args[@]}" "$dest"
EOF
# python3 slo_gate.py --unit NAME ...: exit with STUB_GATE_<NAME>.
cat > "$stub/python3" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == */slo_gate.py ]]; then
  shift
  while [[ \$# -gt 0 ]]; do
    if [[ "\$1" == --unit ]]; then unit="\${2//-/_}"; fi
    if [[ "\$1" == --deployed-at || "\$1" == --duration || "\$1" == --max-hold ]]; then echo "\$1 \$2" >> "\$STUB_LOG"; fi
    shift
  done
  var="STUB_GATE_\${unit}"
  exit "\${!var:-0}"
fi
exec "$real_python" "\$@"
EOF
chmod +x "$stub/"*

export PATH="$stub:$PATH" STUB_LOG="$test_root/log" UNIT_SSH_TARGET=root@cp.example UNIT_SSH_KEY=/dev/null \
  RUNNER_TEMP="$test_root"
fail() { echo "FAIL: $*" >&2; exit 1; }

plan="$test_root/plan.json"
cat > "$plan" <<'EOF'
{
  "action": "deploy",
  "release_id": "0123456789abcdef0123456789abcdef01234567",
  "expect_current": "00000000000000000000000000000000000000b0",
  "commit": "1111111111111111111111111111111111111111",
  "units": ["apid", "meterd"],
  "only": ["apid", "meterd"],
  "verify": ["apid=http://127.0.0.1:9101/readyz", "?apid-clone-worker"],
  "gate": [["--unit", "apid"], ["--unit", "meterd"]],
  "replace": {"bin/apid": "bin/apid", "bin/deployctl": "bin/deployctl", "unit-versions.json": "unit-versions.json"}
}
EOF
stage=/var/tmp/gregale-units.AbC123

# 1. The activation script overlays, deploys only the plan's services with
#    the compare-and-swap, gates each unit and rolls back on a failed gate.
bash "$helper" script "$plan" "$stage" "$test_root/activate.sh"
bash -n "$test_root/activate.sh" || fail "activation script does not parse"
for want in \
  "$stage/bin/deployctl unit-overlay --release-id 0123456789abcdef0123456789abcdef01234567 --expect-current 00000000000000000000000000000000000000b0 --commit 1111111111111111111111111111111111111111" \
  "bin/apid=$stage/bin/apid" \
  "/opt/faas/releases/0123456789abcdef0123456789abcdef01234567/bin/deployctl deploy --only 'apid,meterd' --expect-current 00000000000000000000000000000000000000b0 0123456789abcdef0123456789abcdef01234567" \
  "unit-release-verify.sh /opt/faas/releases/0123456789abcdef0123456789abcdef01234567 apid=http://127.0.0.1:9101/readyz \\?apid-clone-worker" \
  "$stage/bin/deployctl rollback --only 'apid,meterd' --expect-current 0123456789abcdef0123456789abcdef01234567 --to 00000000000000000000000000000000000000b0"; do
  grep -Fq -- "$want" "$test_root/activate.sh" || fail "activation script lacks: $want"
done

# 1a. A plan with a data step overlays bin/migrate, which deployctl deploy
#     runs before it activates anything.
jq '.replace["bin/migrate"] = "bin/migrate" | .gate += [["--unit", "migrations"]]' "$plan" > "$test_root/data.json"
bash "$helper" script "$test_root/data.json" "$stage" "$test_root/data.sh"
grep -Fq "bin/migrate=$stage/bin/migrate" "$test_root/data.sh" || fail "data step does not overlay bin/migrate"

# 1b. Migrations alone restart and verify no unit.
jq '.units = [] | .only = [] | .verify = [] | .replace = {"bin/migrate": "bin/migrate", "bin/deployctl": "bin/deployctl", "unit-versions.json": "unit-versions.json"}' \
  "$plan" > "$test_root/data-only.json"
bash "$helper" script "$test_root/data-only.json" "$stage" "$test_root/data-only.sh"
bash -n "$test_root/data-only.sh" || fail "data-only activation script does not parse"
grep -Fq "deploy --only '' --expect-current" "$test_root/data-only.sh" || fail "data-only plan does not deploy with an empty --only"
if grep -q unit-release-verify "$test_root/data-only.sh"; then fail "data-only plan verifies units it did not restart"; fi

# 2. A converge plan re-activates the active overlay without assembling one.
jq '.action = "converge" | .replace = {} | .release_id = .expect_current' "$plan" > "$test_root/converge.json"
bash "$helper" script "$test_root/converge.json" "$stage" "$test_root/converge.sh"
if grep -q unit-overlay "$test_root/converge.sh"; then fail "converge assembles an overlay"; fi
grep -Fq "deploy --only 'apid,meterd'" "$test_root/converge.sh" || fail "converge does not re-run deploy"
# Its plan names no release before the active one: deployctl takes the one
# its activation history records, and never the active release itself.
grep -Fq "rollback --only 'apid,meterd' --expect-current 00000000000000000000000000000000000000b0" "$test_root/converge.sh" \
  || fail "converge rollback does not expect the active release"
if grep -q -- "--to" "$test_root/converge.sh"; then fail "converge rolls back to the release it reruns"; fi

# 3. Unsafe plan values are refused.
jq '.release_id = "../../etc"' "$plan" > "$test_root/bad.json"
if bash "$helper" script "$test_root/bad.json" "$stage" "$test_root/x.sh" 2>/dev/null; then fail "bad release id accepted"; fi
if bash "$helper" script "$plan" "/tmp/elsewhere" "$test_root/x.sh" 2>/dev/null; then fail "bad stage accepted"; fi

# 4. The SLO gate: one process per unit, worst result wins.
gate_dirs() { find /var/tmp -maxdepth 1 -name 'gregale-slo.*' 2>/dev/null | sort; }
gate_dirs_before="$(gate_dirs)"
gate() { env "$@" bash "$helper" gate "$plan" 1700000000 follower >/dev/null 2>&1; }
gate STUB_GATE_apid=0 STUB_GATE_meterd=0 || fail "passing gates did not promote"
grep -q -- "--deployed-at 1700000000" "$STUB_LOG" || fail "gate did not pass --deployed-at"

# 4a. Canary and follower gates take their durations from the one config.
read -r canary_duration canary_hold follower_duration follower_hold < <(jq -r \
  '[.gates.canary.duration, .gates.canary.max_hold, .gates.follower.duration, .gates.follower.max_hold] | join(" ")' \
  "$repo_root/scripts/ci/release_units.json")
for kind in canary follower; do
  : > "$STUB_LOG"
  bash "$helper" gate "$plan" 1700000000 "$kind" >/dev/null 2>&1 || fail "$kind gate failed"
  if [[ "$kind" == canary ]]; then want_duration="$canary_duration" want_hold="$canary_hold"
  else want_duration="$follower_duration" want_hold="$follower_hold"; fi
  [[ "$(grep -c -- "--duration $want_duration" "$STUB_LOG")" == 2 ]] || fail "$kind gate did not run --duration $want_duration per unit"
  [[ "$(grep -c -- "--max-hold $want_hold" "$STUB_LOG")" == 2 ]] || fail "$kind gate did not run --max-hold $want_hold per unit"
done
[[ "$canary_duration" != "$follower_duration" ]] || fail "canary and follower gates are the same"
if bash "$helper" gate "$plan" 1700000000 10m >/dev/null 2>&1; then fail "a raw duration was accepted as a gate kind"; fi
for case in "1 0 1" "0 3 3" "2 0 3" "1 3 1"; do
  read -r apid meterd want <<<"$case"
  status=0
  gate STUB_GATE_apid="$apid" STUB_GATE_meterd="$meterd" || status=$?
  [[ "$status" == "$want" ]] || fail "gate apid=$apid meterd=$meterd exited $status, want $want"
done
jq '.gate = []' "$plan" > "$test_root/nogate.json"
bash "$helper" gate "$test_root/nogate.json" 1700000000 canary >/dev/null || fail "an empty gate did not pass"
jq '.gate = [["--unit", "apid; rm -rf /"]]' "$plan" > "$test_root/evil.json"
status=0
bash "$helper" gate "$test_root/evil.json" 1700000000 canary >/dev/null 2>&1 || status=$?
[[ "$status" == 2 ]] || fail "an unsafe gate argument was not refused (exit $status)"
[[ "$(gate_dirs)" == "$gate_dirs_before" ]] \
  || fail "the gate left its directory on the host"

# 5. Rollback runs as root on the deploy host and re-verifies the units.
: > "$STUB_LOG"
# Capture the rollback script instead of running it against /opt/faas.
cat > "$stub/bash-capture" <<'EOF'
#!/usr/bin/env bash
cat > "$CAPTURE"
EOF
chmod +x "$stub/bash-capture"
# Literal $(...) on purpose: rewrite the helper's remote shell to the capture stub.
# shellcheck disable=SC2016
sed 's#"$(as_root "$target") -s"#"CAPTURE=$CAPTURE bash-capture"#' "$helper" > "$test_root/helper-capture.sh"
CAPTURE="$test_root/rollback.sh" UNIT_SSH_TARGET=deploy@node.example \
  bash "$test_root/helper-capture.sh" rollback "$plan" "$stage"
# The rollback restores the plan's base, not the newest other directory:
# after deploy A, roll back, deploy B, that is A (cd-controlplane run
# 39480533579). It refuses if another release replaced the plan's since.
grep -Fq "$stage/bin/deployctl rollback --only 'apid,meterd' --expect-current 0123456789abcdef0123456789abcdef01234567 --to 00000000000000000000000000000000000000b0" "$test_root/rollback.sh" \
  || fail "rollback does not restore the plan's base for the plan's services with the staged deployctl"
CAPTURE="$test_root/rollback-converge.sh" UNIT_SSH_TARGET=deploy@node.example \
  bash "$test_root/helper-capture.sh" rollback "$test_root/converge.json" "$stage"
grep -Fq "$stage/bin/deployctl rollback --only 'apid,meterd' --expect-current 00000000000000000000000000000000000000b0" "$test_root/rollback-converge.sh" \
  || fail "converge rollback does not expect the active release"
if grep -q -- "--to" "$test_root/rollback-converge.sh"; then fail "converge rollback names the release it reruns as the target"; fi
grep -Fq "unit-release-verify.sh \"\$(readlink -f /opt/faas/current)\" apid=http://127.0.0.1:9101/readyz" "$test_root/rollback.sh" \
  || fail "rollback does not verify the restored release"
grep -q "sudo -n bash" "$helper" || fail "non-root hosts must run through sudo"
CAPTURE="$test_root/rollback-data.sh" UNIT_SSH_TARGET=deploy@node.example \
  bash "$test_root/helper-capture.sh" rollback "$test_root/data-only.json" "$stage"
bash -n "$test_root/rollback-data.sh" || fail "data-only rollback script does not parse"
if grep -q unit-release-verify "$test_root/rollback-data.sh"; then fail "data-only rollback verifies no unit it restored"; fi

# 6. Staging copies the plan's binaries, and tools/migrate as bin/migrate
#    when the plan applies migrations.
units="$test_root/units"
mkdir -p "$units/bin" "$units/tools"
for f in bin/apid bin/meterd tools/migrate tools/deployctl; do echo "$f" > "$units/$f"; done
echo '{}' > "$test_root/unit-versions.json"
remote_stage="$(bash "$helper" stage "$test_root/data.json" "$units" "$units/tools/deployctl")"
[[ "$(cat "$remote_stage/bin/migrate")" == tools/migrate && "$(cat "$remote_stage/bin/apid")" == bin/apid ]] \
  || fail "staging did not place tools/migrate at bin/migrate"
bash "$helper" cleanup "$remote_stage"
remote_stage="$(bash "$helper" stage "$plan" "$units" "$units/tools/deployctl")"
[[ ! -e "$remote_stage/bin/migrate" ]] || fail "a plan without a data step staged bin/migrate"
bash "$helper" cleanup "$remote_stage"
[[ ! -e "$remote_stage" ]] || fail "cleanup left the stage dir"

# 7. Host configuration (docs/adr/20261018133100-host-configuration-lane.md):
#    the record is written only whole and valid, with the contract.
record="$test_root/record.json"
export HOST_CONFIG_DIR="$test_root/bootstrap"
jq -n '{format_version: 1, kind: "gregale-host-config", group: "control_plane",
        commit: "1111111111111111111111111111111111111111", globals: "sha256:g",
        order: ["postgres_capacity"], roles: {postgres_capacity: "sha256:p"},
        environment: ("sha256:" + ("e" * 64))}' > "$record"
contract="sha256:$(printf 'c%.0s' {1..64})"
bash "$helper" host-record "$record" "$contract" || fail "host-record refused a valid record"
cmp -s <(jq -S . "$record") <(jq -S . "$HOST_CONFIG_DIR/control-plane-roles.json") || fail "host-record changed the record"
[[ "$(cat "$HOST_CONFIG_DIR/control-plane-bootstrap-contract.sha256")" == "$contract" ]] || fail "host-record did not write the contract"
[[ "$(stat -c %a "$HOST_CONFIG_DIR" 2>/dev/null || stat -f %Lp "$HOST_CONFIG_DIR")" == 700 ]] || fail "host-record left the record directory readable"
jq 'del(.environment)' "$record" > "$test_root/no-env.json"
if bash "$helper" host-record "$test_root/no-env.json" "$contract" 2>/dev/null; then fail "a record without its environment hash was accepted"; fi
if bash "$helper" host-record "$record" "sha256:'; rm -rf /'" 2>/dev/null; then fail "an unsafe contract was accepted"; fi
cmp -s <(jq -S . "$record") <(jq -S . "$HOST_CONFIG_DIR/control-plane-roles.json") || fail "a refused record replaced the old one"

# 7a. host-stage ships deployctl alone; the restart script runs
#     restart-changed as root, keeps its report, and marks the activation.
cat > "$units/tools/deployctl" <<'STUB'
#!/usr/bin/env bash
[[ "$*" == "restart-changed --json" ]] || exit 9
echo '{"restarted":[{"service":"meterd","reason":"x","config_only":false}],"dry_run":false}'
STUB
chmod +x "$units/tools/deployctl"
remote_stage="$(bash "$helper" host-stage "$units/tools/deployctl")"
[[ -x "$remote_stage/bin/deployctl" && ! -e "$remote_stage/bin/apid" ]] || fail "host-stage did not stage deployctl alone"
bash "$helper" host-restart-script "$remote_stage" "$test_root/host-restart.sh"
ROLLOUT_ACTIVATION_MARKER="$test_root/activated" bash "$helper" run "$test_root/host-restart.sh" || fail "the restart script failed"
[[ -f "$test_root/activated" ]] || fail "the restart script did not mark the activation"
[[ "$(bash "$helper" host-restarted "$remote_stage" | jq -r '.restarted[0].service')" == meterd ]] || fail "the restart report was lost"
if bash "$helper" host-restart-script "/tmp/elsewhere" "$test_root/x.sh" 2>/dev/null; then fail "a bad stage was accepted"; fi
bash "$helper" cleanup "$remote_stage"
unset HOST_CONFIG_DIR

# 8. preflight-state: the deploy lock and Prometheus's active alerts, as one
#    JSON object, whatever the remote could not read.
cat > "$stub/flock" <<'EOF'
#!/usr/bin/env bash
[[ "$1 $2 $3" == "-n -E 75" ]] || exit 2
exit "${STUB_FLOCK:-0}"
EOF
cat > "$stub/curl" <<'EOF'
#!/usr/bin/env bash
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in -o) out="$2"; shift 2 ;; -*) shift ;; *) url="$1"; shift ;; esac
done
[[ "$url" == http://127.0.0.1:9095/api/v1/alerts ]] || { echo "unexpected url $url" >&2; exit 2; }
[[ -z "${STUB_CURL_FAIL:-}" ]] || { echo "curl: (7) Failed to connect" >&2; exit 7; }
if [[ -n "${STUB_ALERTS_FILE:-}" ]]; then cp "$STUB_ALERTS_FILE" "$out"; else printf '%s' "$STUB_ALERTS" > "$out"; fi
EOF
chmod +x "$stub/flock" "$stub/curl"
export STUB_ALERTS='{"status":"success","data":{"alerts":[{"labels":{"alertname":"FaasDaemonDown","severity":"page"},"state":"firing","activeAt":"2026-10-20T10:00:00Z"}]}}'
state="$(bash "$helper" preflight-state)"
jq -e '.lock == "free" and .prometheus.ok and (.prometheus.alerts | length) == 1 and .prometheus.alerts[0].labels.alertname == "FaasDaemonDown"' <<<"$state" >/dev/null \
  || fail "preflight-state did not report a free lock and the alert: $state"
state="$(STUB_FLOCK=75 bash "$helper" preflight-state)"
jq -e '.lock == "held"' <<<"$state" >/dev/null || fail "a held deploy lock was not reported: $state"
state="$(STUB_FLOCK=1 bash "$helper" preflight-state)"
jq -e '.lock == "unknown"' <<<"$state" >/dev/null || fail "an unreadable lock was not unknown: $state"
state="$(STUB_CURL_FAIL=1 bash "$helper" preflight-state)"
jq -e '.lock == "free" and (.prometheus.ok | not) and (.prometheus.error | test("Failed to connect"))' <<<"$state" >/dev/null \
  || fail "an unreachable Prometheus was not reported: $state"
state="$(STUB_ALERTS='{"status":"error","error":"bad"}' bash "$helper" preflight-state)"
jq -e '(.prometheus.ok | not)' <<<"$state" >/dev/null || fail "a failed Prometheus answer counted as ok: $state"
# More than one command-line argument can hold (MAX_ARG_STRLEN is 128 KiB,
# which on Linux also bounds an environment string: the fixture is a file).
jq -cn '{status: "success", data: {alerts: [range(3000) | {labels: {alertname: "FaasX", severity: "warn", instance: ("i-" + tostring)}, state: "firing"}]}}' > "$test_root/big-alerts.json"
(( $(wc -c < "$test_root/big-alerts.json") > 131072 )) || fail "the large alert fixture is not larger than one argument"
state="$(STUB_ALERTS_FILE="$test_root/big-alerts.json" bash "$helper" preflight-state)"
jq -e '(.prometheus.alerts | length) == 3000' <<<"$state" >/dev/null || fail "a large alert list was not passed through"
if PROMETHEUS_URL=http://10.0.0.1:9095 bash "$helper" preflight-state >/dev/null 2>&1; then
  fail "a non-loopback Prometheus URL was accepted"
fi

# 9. The post-deploy watch (docs/adr/20261020214632-automatic-unit-deploy-
#    safety-rails.md): the record on the control plane, the watch over its
#    units, and the rollback to each host's known-good release.
export WATCH_RECORD="$test_root/deploy/unit-watch.json" WATCH_OWNER=500 UNITS_SHA=1111111111111111111111111111111111111111
releases="$test_root/releases"
known_good=00000000000000000000000000000000000000b0
mkdir -p "$releases/$known_good" "$releases/0123456789abcdef0123456789abcdef01234567"
for r in "$releases"/*; do
  jq -n '{git_sha: "2222222222222222222222222222222222222222"}' > "$r/release-manifest.json"
done
real_readlink="$(command -v readlink)"
cat > "$stub/readlink" <<EOF
#!/usr/bin/env bash
if [[ "\$*" == "-f /opt/faas/current" ]]; then cat "$test_root/current"; exit 0; fi
exec "$real_readlink" "\$@"
EOF
chmod +x "$stub/readlink"
printf '%s\n' "$releases/$known_good" > "$test_root/current"
watch_dirs() { find /var/tmp -maxdepth 1 -name 'gregale-watch.*' 2>/dev/null | sort; }
watch_dirs_before="$(watch_dirs)"

bash "$helper" watch-open "$plan" control-plane || fail "watch-open refused the first deploy"
jq -e --arg kg "$known_good" '.owner_run == "500" and .hosts["control-plane"].known_good == $kg
    and .hosts["control-plane"].releases == ["0123456789abcdef0123456789abcdef01234567"]
    and .hosts["control-plane"].commits == ["1111111111111111111111111111111111111111"]' "$WATCH_RECORD" >/dev/null \
  || fail "watch-open did not record the release over its known-good one: $(cat "$WATCH_RECORD")"
[[ "$(bash "$helper" watch-record | jq -r .owner_run)" == 500 ]] || fail "watch-record did not print the record"
state="$(bash "$helper" preflight-state)"
jq -e '.watch.owner_run == "500" and (.watch.hosts["control-plane"].releases | length) == 1' <<<"$state" >/dev/null \
  || fail "preflight-state does not report the watch record: $state"
[[ "$(watch_dirs)" == "$watch_dirs_before" ]] || fail "watch-open left its temp dir"

# A node: its pinned target rides in the record.
jq -n '{node: "fsn-2", ssh_host: "10.0.0.2", ssh_user: "root", ssh_port: "22", ssh_host_key_sha256: ("SHA256:" + ("x" * 43))}' > "$test_root/target.json"
bash "$helper" watch-open "$plan" fsn-2 "$test_root/target.json" || fail "watch-open refused a node"
jq -e '.hosts["fsn-2"].target.node == "fsn-2" and .order == ["control-plane", "fsn-2"]' "$WATCH_RECORD" >/dev/null \
  || fail "the node's target or order was not recorded"
if bash "$helper" watch-open "$plan" '../etc' 2>/dev/null; then fail "an unsafe host label was accepted"; fi

# watch: one slo_gate per recorded unit, in watch mode, worst result wins.
: > "$STUB_LOG"
STUB_GATE_apid=0 STUB_GATE_meterd=0 bash "$helper" watch >/dev/null || fail "a clean watch did not pass"
[[ "$(grep -c -- '--max-hold 1800s' "$STUB_LOG")" == 2 ]] || fail "watch did not hold each rule for its own for: $(cat "$STUB_LOG")"
for case in "1 4 1" "4 0 4" "3 4 3" "0 0 0"; do
  read -r apid meterd want <<<"$case"
  status=0
  STUB_GATE_apid="$apid" STUB_GATE_meterd="$meterd" bash "$helper" watch >/dev/null 2>&1 || status=$?
  [[ "$status" == "$want" ]] || fail "watch with apid=$apid meterd=$meterd exited $status, want $want"
done
WATCH_OWNER=501 bash "$helper" watch >/dev/null 2>&1 && fail "a run that does not own the record watched it"
status=0; WATCH_OWNER=501 bash "$helper" watch >/dev/null 2>&1 || status=$?
[[ "$status" == 4 ]] || fail "watch by a non-owner exited $status, want 4"

# watch-rollback: back to known-good, only when the host runs what the record expects.
cat > "$units/tools/deployctl" <<'STUB'
#!/usr/bin/env bash
echo "deployctl $*" >> "$STUB_LOG"
STUB
chmod +x "$units/tools/deployctl"
jq '.hosts["control-plane"]' "$WATCH_RECORD" > "$test_root/entry.json"
remote_stage="$(bash "$helper" watch-stage "$units/tools/deployctl")"
[[ -x "$remote_stage/bin/deployctl" && -f "$remote_stage/unit-release-verify.sh" ]] || fail "watch-stage did not stage deployctl and the verifier"
printf '%s\n' "$releases/0123456789abcdef0123456789abcdef01234567" > "$test_root/current"
: > "$STUB_LOG"
UNIT_VERIFY_TIMEOUT=1 bash "$helper" watch-rollback "$test_root/entry.json" "$remote_stage" >/dev/null 2>&1 || true
grep -Fq "deployctl rollback --only 'apid,meterd' --expect-current 0123456789abcdef0123456789abcdef01234567 --to $known_good" "$STUB_LOG" \
  || grep -Fq "deployctl rollback --only apid,meterd --expect-current 0123456789abcdef0123456789abcdef01234567 --to $known_good" "$STUB_LOG" \
  || fail "watch-rollback did not roll back to known-good: $(cat "$STUB_LOG")"
printf '%s\n' "$releases/$known_good" > "$test_root/current"
: > "$STUB_LOG"
bash "$helper" watch-rollback "$test_root/entry.json" "$remote_stage" >/dev/null || fail "a host on known-good was not left as it is"
grep -q deployctl "$STUB_LOG" && fail "watch-rollback restarted a host already on known-good"
printf '%s\n' "$releases/ffffffffffffffffffffffffffffffffffffffff" > "$test_root/current"
status=0; bash "$helper" watch-rollback "$test_root/entry.json" "$remote_stage" >/dev/null 2>&1 || status=$?
[[ "$status" == 5 ]] || fail "a host changed since the record was not left alone (exit $status)"
grep -q deployctl "$STUB_LOG" && fail "watch-rollback touched a host changed since the record"
bash "$helper" cleanup "$remote_stage"

# watch-revert after a failed deploy; watch-adopt; watch-close by its owner only.
printf '%s\n' "$releases/$known_good" > "$test_root/current"
bash "$helper" watch-revert fsn-2 || fail "watch-revert failed"
jq -e '.hosts | has("fsn-2") | not' "$WATCH_RECORD" >/dev/null || fail "a node back on known-good stayed in the record"
WATCH_OWNER=777 bash "$helper" watch-adopt >/dev/null || fail "watch-adopt failed"
jq -e '.owner_run == "777" and (.watch_from | type) == "number"' "$WATCH_RECORD" >/dev/null || fail "watch-adopt did not take the record"
status=0; bash "$helper" watch-close >/dev/null 2>&1 || status=$?
[[ "$status" == 4 && -f "$WATCH_RECORD" ]] || fail "a non-owner closed the record (exit $status)"
WATCH_OWNER=777 bash "$helper" watch-close >/dev/null || fail "the owner could not close the record"
[[ ! -f "$WATCH_RECORD" ]] || fail "watch-close left the record"
[[ "$(bash "$helper" watch-record)" == null ]] || fail "watch-record of no record is not null"

# The cap: at most max_unwatched releases per host.
printf '%s\n' "$releases/$known_good" > "$test_root/current"
bash "$helper" watch-open "$plan" control-plane || fail "watch-open (1)"
jq '.release_id = "0123456789abcdef0123456789abcdef0123456a" | .expect_current = "0123456789abcdef0123456789abcdef01234567"' "$plan" > "$test_root/plan2.json"
printf '%s\n' "$releases/0123456789abcdef0123456789abcdef01234567" > "$test_root/current"
bash "$helper" watch-open "$test_root/plan2.json" control-plane || fail "watch-open (2)"
jq '.release_id = "0123456789abcdef0123456789abcdef0123456b" | .expect_current = "0123456789abcdef0123456789abcdef0123456a"' "$plan" > "$test_root/plan3.json"
mkdir -p "$releases/0123456789abcdef0123456789abcdef0123456a"
cp "$releases/$known_good/release-manifest.json" "$releases/0123456789abcdef0123456789abcdef0123456a/"
printf '%s\n' "$releases/0123456789abcdef0123456789abcdef0123456a" > "$test_root/current"
status=0; bash "$helper" watch-open "$test_root/plan3.json" control-plane 2>/dev/null || status=$?
[[ "$status" == 3 ]] || fail "a third unwatched release was accepted (exit $status)"
rm -f "$WATCH_RECORD"

# pin-node keeps only keys with the pinned fingerprint.
cat > "$stub/ssh-keyscan" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "10.0.0.2 ssh-ed25519 AAAAgood" "10.0.0.2 ssh-rsa AAAAother"
EOF
cat > "$stub/ssh-keygen" <<'EOF'
#!/usr/bin/env bash
line="$(cat)"
case "$line" in *AAAAgood*) echo "256 SHA256:$(printf 'g%.0s' {1..43}) host (ED25519)" ;; *) echo "3072 SHA256:$(printf 'o%.0s' {1..43}) host (RSA)" ;; esac
EOF
chmod +x "$stub/ssh-keyscan" "$stub/ssh-keygen"
bash "$helper" pin-node 10.0.0.2 22 "SHA256:$(printf 'g%.0s' {1..43})" "$test_root/pinned" || fail "pin-node refused the pinned key"
[[ "$(cat "$test_root/pinned")" == "10.0.0.2 ssh-ed25519 AAAAgood" ]] || fail "pin-node kept another key: $(cat "$test_root/pinned")"
if bash "$helper" pin-node 10.0.0.2 22 "SHA256:$(printf 'z%.0s' {1..43})" "$test_root/pinned" 2>/dev/null; then
  fail "pin-node accepted a host without the pinned key"
fi
unset WATCH_RECORD WATCH_OWNER UNITS_SHA

# In a job (RUNNER_TEMP set) every ssh of the job shares one connection per
# host; UNIT_SSH_MUX_DIR= turns that off.
cat > "$stub/ssh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_LOG.opts"
EOF
chmod +x "$stub/ssh"
short_temp="$(mktemp -d /tmp/ud.XXXXXX)"
: > "$STUB_LOG.opts"
RUNNER_TEMP="$short_temp" bash "$helper" cleanup /var/tmp/gregale-units.Mux1
grep -Fq -- "-o ControlMaster=auto -o ControlPath=$short_temp/ssh-mux/%C -o ControlPersist=120" "$STUB_LOG.opts" \
  || fail "ssh does not reuse one connection per host in a job: $(cat "$STUB_LOG.opts")"
[[ "$(stat -c %a "$short_temp/ssh-mux" 2>/dev/null || stat -f %Lp "$short_temp/ssh-mux")" == 700 ]] \
  || fail "the connection directory is not private"
rm -rf -- "$short_temp"
: > "$STUB_LOG.opts"
RUNNER_TEMP=/tmp UNIT_SSH_MUX_DIR='' bash "$helper" cleanup /var/tmp/gregale-units.Mux2
if grep -Fq ControlMaster "$STUB_LOG.opts"; then fail "UNIT_SSH_MUX_DIR= still shares connections"; fi
# The runners' /opt/actions-runner-2/_work/_temp/ssh-mux fits; a longer
# directory would make ssh refuse to connect, so it connects without sharing.
: > "$STUB_LOG.opts"
UNIT_SSH_MUX_DIR="/opt/actions-runner-2/_work/_temp/$(printf 'x%.0s' {1..20})" bash "$helper" cleanup /var/tmp/gregale-units.Mux3
if grep -Fq ControlMaster "$STUB_LOG.opts"; then fail "a socket path ssh cannot bind still shares connections"; fi
[[ -s "$STUB_LOG.opts" ]] || fail "a long socket directory stopped ssh"

echo "unit_deploy tests passed"
