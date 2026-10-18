package main

import (
	"strings"
	"testing"
)

// adr: 20261018133100

// TestHostConfigConvergesBeforeTheUnitsAndOnlyChangedRoles: a control-plane
// host role change rides the unit release. cd-controlplane converges the
// roles whose hash differs from the host's record before deploy-units
// restarts anything, through the ADR-580 command the signed release
// carries, never a runner-built binary or a second Ansible runner.
func TestHostConfigConvergesBeforeTheUnitsAndOnlyChangedRoles(t *testing.T) {
	workflow := readWorkflow(t, "cd-controlplane.yml")
	job := jobBlock(t, workflow, "converge-host-config")
	units := jobBlock(t, workflow, "deploy-units")
	if !strings.Contains(job, "if: github.repository == 'poyrazK/faas' && inputs.units_run_id != '' && inputs.units_lane == 'control-plane'") {
		t.Error("host configuration runs for the control-plane lane of a unit release only")
	}
	if !strings.Contains(units, "needs: [converge-host-config]") ||
		!strings.Contains(units, "needs.converge-host-config.result == 'success' || needs.converge-host-config.result == 'skipped'") {
		t.Error("deploy-units must wait for host configuration and never run after it failed")
	}
	if !strings.Contains(job, "group: cd-cp-units-${{ inputs.deploy_environment }}") {
		t.Error("host configuration must share the unit lane's concurrency group")
	}
	for _, forbidden := range []string{"actions/setup-go", "go build", "ansible-playbook -i", "systemctl restart"} {
		if strings.Contains(job, forbidden) {
			t.Errorf("converge-host-config must not run %q: it converges through gregalectl and restarts through deployctl", forbidden)
		}
	}
	orderedIn(t, "converge-host-config", job,
		"workflow-src/scripts/ci/verify_unit_release.sh",
		"jq -e '.host_config.version'",
		"ref: ${{ steps.verify.outputs.git_sha }}",
		"scripts/ci/unit_deploy.sh host-state",
		"scripts/ci/plan_host_config.py",
		`--previous-release "$previous"`,
		"if: steps.plan.outputs.action == 'converge'",
		`scripts/ci/unit_deploy.sh host-manifest "$work/host-state.json"`,
		`scripts/ci/unit_deploy.sh host-stage "$UNITS_DIR/tools/deployctl"`,
		`echo "DEPLOYED_AT=$(scripts/ci/unit_deploy.sh now)"`,
		`"$UNITS_DIR/tools/gregalectl" deploy converge-control-plane`,
		`--roles "$ROLES"`,
		"scripts/ci/unit_deploy.sh host-restart-script",
		"scripts/ci/observe_remote_rollout.sh",
		"scripts/ci/unit_deploy.sh host-restarted",
		`plan_host_config.py --gate-from`,
		`scripts/ci/unit_deploy.sh gate "$HOST_CONFIG_WORK/gate.json" "$DEPLOYED_AT" "$SLO_GATE"`,
		`scripts/ci/unit_deploy.sh host-record`,
	)
	if !strings.Contains(job, `echo "::error::host configuration converges only from the fleet runner`) {
		t.Error("a hosted runner cannot reach the fleet's private names; host configuration must stop, not skip")
	}
}

// TestHostConfigRollsBackToTheRecordedContent: any failure after the roles
// started converging (the convergence, a restart, the SLO gate) converges
// the same roles at the commit the host's record names and restarts what
// that changes; the record is written only after the gate passed.
func TestHostConfigRollsBackToTheRecordedContent(t *testing.T) {
	job := jobBlock(t, readWorkflow(t, "cd-controlplane.yml"), "converge-host-config")
	orderedIn(t, "converge-host-config", job,
		`echo "HOST_CONFIG_STARTED=true"`,
		`"$UNITS_DIR/tools/gregalectl" deploy converge-control-plane`,
		"- name: Hold the control plane on the SLO gate",
		"- name: Record the converged roles",
		"- name: Converge the roles back after a failure",
		"if: failure() && env.HOST_CONFIG_STARTED == 'true'",
		`git worktree add --detach "$work/previous" "$PREVIOUS"`,
		`--repo-root "$work/previous"`,
		`scripts/ci/unit_deploy.sh run "$script"`,
		"exit 1",
	)
	helper := readRepoFile(t, "scripts", "ci", "unit_deploy.sh")
	orderedIn(t, "unit_deploy.sh host-restart-script", helper,
		"cmd_host_restart_script() {",
		"bin/deployctl restart-changed --json",
		"ROLLOUT_ACTIVATION_MARKER",
	)
	orderedIn(t, "unit_deploy.sh host-record", helper,
		"cmd_host_record() {",
		`.environment | test("^sha256:[0-9a-f]{64}$")`,
		"control-plane-roles.json",
		"control-plane-bootstrap-contract.sha256",
	)
}

// TestUnitReleaseCarriesTheConvergeCommandOnlyWithHostConfig: the
// converge command rides the signed release, built at its commit, only when
// the range changed a control-plane host role, and the verifier refuses a
// host-config step without it.
func TestUnitReleaseCarriesTheConvergeCommandOnlyWithHostConfig(t *testing.T) {
	build := readRepoFile(t, "scripts", "build-unit-release.sh")
	orderedIn(t, "build-unit-release.sh", build,
		`.host_config.version // empty`,
		"-o \"$work/tools/gregalectl\" ./cmd/gregalectl",
		`manifest["host_config"]`,
	)
	verifier := readRepoFile(t, "scripts", "ci", "verify_unit_release.sh")
	if !strings.Contains(verifier, `(.tools.gregalectl.sha256 | type == "string")`) {
		t.Error("verify_unit_release.sh must refuse a host-config step without tools/gregalectl")
	}
}
