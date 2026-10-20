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

echo "unit_deploy tests passed"
