package main

import (
	"strings"
	"testing"
)

// Per-unit control-plane releases (docs/adr/1650-per-unit-control-plane-releases.md).

// jobBlock returns a top-level job's text, up to the next top-level job.
func jobBlock(t *testing.T, workflow, job string) string {
	t.Helper()
	start := strings.Index(workflow, "\n  "+job+":\n")
	if start < 0 {
		t.Fatalf("job %q not found", job)
	}
	rest := workflow[start+1:]
	lines := strings.Split(rest, "\n")
	end := len(rest)
	offset := len(lines[0]) + 1
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			end = offset
			break
		}
		offset += len(line) + 1
	}
	return rest[:end]
}

// orderedIn fails unless the needles occur in text in order, each after the
// previous one.
func orderedIn(t *testing.T, where, text string, needles ...string) {
	t.Helper()
	from := 0
	for _, needle := range needles {
		at := strings.Index(text[from:], needle)
		if at < 0 {
			if strings.Contains(text, needle) {
				t.Fatalf("%s runs %q out of order", where, needle)
			}
			t.Fatalf("%s is missing %q", where, needle)
		}
		from += at + len(needle)
	}
}

// TestUnitReleaseIsSignedOnlyByMainAndVerifiedBeforeUse: the unit lanes
// install binaries as root on the control plane and compute nodes. They must
// come from release-units.yml on main, match the signed manifest file by
// file, and be verified, by the workflow's own copy of the verifier, before
// the release commit is checked out or anything reaches a host.
func TestUnitReleaseIsSignedOnlyByMainAndVerifiedBeforeUse(t *testing.T) {
	release := readWorkflow(t, "release-units.yml")
	for _, required := range []string{
		"cosign sign-blob --yes",
		`--certificate-identity "https://github.com/${GITHUB_REPOSITORY}/.github/workflows/release-units.yml@refs/heads/main"`,
		"scripts/ci/affected_units.py --base",
		"scripts/build-unit-release.sh",
		"if: steps.detect.outputs.eligible == 'true'",
	} {
		if !strings.Contains(release, required) {
			t.Errorf("release-units.yml is missing %q", required)
		}
	}
	if strings.Contains(release, "cancel-in-progress: true") && !strings.Contains(jobBlock(t, release, "build"), "cancel-in-progress: true") {
		t.Error("only the build job may cancel in progress; a started deploy must finish")
	}

	verifier := readRepoFile(t, "scripts", "ci", "verify_unit_release.sh")
	if !strings.Contains(verifier, `identity="${UNIT_RELEASE_IDENTITY:-https://github.com/poyrazK/faas/.github/workflows/release-units.yml@refs/heads/main}"`) {
		t.Error("verify_unit_release.sh must accept only release-units.yml on main")
	}
	orderedIn(t, "verify_unit_release.sh", verifier,
		"sha256sum -c --quiet SHA256SUMS",
		`--certificate-identity "$identity"`,
		`cmp "$manifest" "$dir/units-manifest.json"`,
		`(cd "$dir/unpacked" && sha256sum -c --quiet -)`,
		`-ne "$(jq '.units | length' "$manifest")"`,
	)

	for _, job := range []struct{ file, name string }{
		{"cd-controlplane.yml", "deploy-units"},
		{"cd-edge-units.yml", "plan"},
		{"cd-edge-node.yml", "deploy"},
	} {
		block := jobBlock(t, readWorkflow(t, job.file), job.name)
		orderedIn(t, job.file+" "+job.name, block,
			"sparse-checkout: scripts/ci/verify_unit_release.sh",
			"workflow-src/scripts/ci/verify_unit_release.sh",
			"ref: ${{ steps.verify.outputs.git_sha }}",
			"scripts/ci/unit_deploy.sh state",
		)
	}
}

// shellFunc returns a bash function's text, from its "NAME() {" line to the
// closing brace at the start of a line.
func shellFunc(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, "\n"+name+"() {\n")
	if start < 0 {
		t.Fatalf("shell function %q not found", name)
	}
	body := script[start+1:]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatalf("shell function %q is not closed", name)
	}
	return body[:end+2]
}

// TestUnitLaneRestartsOnlyPlannedServicesOverTheExpectedRelease: the host
// step must compare-and-swap against the release the plan was made over,
// restart only planned services, gate each one, and roll back on a failed
// gate. Migrations follow TestUnitLaneMigratesOnlyThroughDeployctlsOwnStep.
func TestUnitLaneRestartsOnlyPlannedServicesOverTheExpectedRelease(t *testing.T) {
	helper := readRepoFile(t, "scripts", "ci", "unit_deploy.sh")
	orderedIn(t, "unit_deploy.sh activation script", helper,
		"deployctl unit-overlay --release-id ${release_id} --expect-current ${expect}",
		"deployctl deploy --only '${only}' --expect-current ${expect} ${release_id}",
		"ROLLOUT_ACTIVATION_MARKER",
		"unit-release-verify.sh ${release_dir}${specs}",
		"deployctl rollback --only '${only}'",
	)
	for _, forbidden := range []string{"converge-control-plane", "kgv rotate", "fleet-seal"} {
		if strings.Contains(helper, forbidden) {
			t.Errorf("the unit lanes must leave %q to the full lane", forbidden)
		}
	}
}

// TestUnitLaneMigratesOnlyThroughDeployctlsOwnStep: the data step
// (docs/adr/20261018084150-unit-lane-expand-migrations.md §3, §4). The unit
// lanes add no migration runner of their own. The host helper only stages
// the signed release's tools/migrate as the overlay's bin/migrate when the
// plan replaces it; `deployctl deploy` (pinned above, before any restart or
// gate) runs it under the deploy and advisory locks. Nothing else in the
// helper, and nothing on rollback, touches the schema. Contract migrations
// stay on the full lane: the verifier and the planner refuse a release that
// carries any migration without a lane-eligible (expand-only) verdict, and
// only the control-plane lane on the control plane applies the data step.
func TestUnitLaneMigratesOnlyThroughDeployctlsOwnStep(t *testing.T) {
	helper := readRepoFile(t, "scripts", "ci", "unit_deploy.sh")
	staging := []string{
		`if jq -e '.replace["bin/migrate"]' "$plan" >/dev/null; then`,
		`install -m 0755 "$units/tools/migrate" "$local_stage/bin/migrate"`,
	}
	orderedIn(t, "unit_deploy.sh stage", shellFunc(t, helper, "cmd_stage"), staging...)
	for i, line := range strings.Split(helper, "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") || !strings.Contains(strings.ToLower(code), "migrat") {
			continue
		}
		if code != staging[0] && code != staging[1] {
			t.Errorf("unit_deploy.sh:%d: the unit lanes apply migrations only through deployctl deploy's own step, found %q", i+1, code)
		}
	}
	for _, line := range staging {
		if got := strings.Count(helper, line); got != 1 {
			t.Errorf("unit_deploy.sh stages bin/migrate %d times with %q, want once in cmd_stage", got, line)
		}
	}
	if strings.Contains(strings.ToLower(helper), "goose") {
		t.Error("the unit lanes must not run a migration runner of their own")
	}
	if strings.Contains(strings.ToLower(shellFunc(t, helper, "cmd_rollback")), "migrat") {
		t.Error("a unit rollback restores binaries, never the schema")
	}

	verifier := readRepoFile(t, "scripts", "ci", "verify_unit_release.sh")
	for _, required := range []string{
		"all(.data.migrations[]; .eligible == true)",
		`(.tools.migrate.sha256 | type == "string")`,
	} {
		if !strings.Contains(verifier, required) {
			t.Errorf("verify_unit_release.sh must refuse migrations without a lane-eligible verdict: missing %q", required)
		}
	}
	planner := readRepoFile(t, "scripts", "ci", "plan_unit_deploy.py")
	for _, required := range []string{
		"not all(m.get(\"eligible\") for m in migrations)",
		"only a full release applies them",
		`lane == "control-plane" and host_role == "control-plane"`,
	} {
		if !strings.Contains(planner, required) {
			t.Errorf("plan_unit_deploy.py must apply only lane-eligible migrations, on the control-plane lane: missing %q", required)
		}
	}
}

// TestUnitLanesGateEveryHostAndRollBack: a unit deploy is not promoted until
// the SLO gate passes; a breach or an inconclusive gate (exit 1 or 3) puts
// the plan's services back on the previous release and fails the run.
func TestUnitLanesGateEveryHostAndRollBack(t *testing.T) {
	for _, job := range []struct{ file, name, activate string }{
		{"cd-controlplane.yml", "deploy-units", "scripts/ci/observe_remote_rollout.sh"},
		{"cd-edge-node.yml", "deploy", `scripts/ci/unit_deploy.sh run "$activation_script"`},
	} {
		block := jobBlock(t, readWorkflow(t, job.file), job.name)
		// Deploy jobs run the signed deployctl from the unit release and
		// build nothing.
		for _, forbidden := range []string{"actions/setup-go", "go build"} {
			if strings.Contains(block, forbidden) {
				t.Errorf("%s %s must not build: found %q", job.file, job.name, forbidden)
			}
		}
		orderedIn(t, job.file+" "+job.name, block,
			`[[ "$SLO_GATE" == canary || "$SLO_GATE" == follower ]]`,
			"scripts/ci/plan_unit_deploy.py",
			`--previous-release "$`,
			`scripts/ci/unit_deploy.sh stage "$UNIT_PLAN" "$UNITS_DIR" "$UNITS_DIR/tools/deployctl"`,
			"scripts/ci/unit_deploy.sh script",
			`echo "DEPLOYED_AT=$(scripts/ci/unit_deploy.sh now)"`,
			job.activate,
			`scripts/ci/unit_deploy.sh gate "$UNIT_PLAN" "$DEPLOYED_AT" "$SLO_GATE" || status=$?`,
			`scripts/ci/unit_deploy.sh rollback "$UNIT_PLAN" "$REMOTE_STAGE"`,
			"back after the SLO gate (exit $status)",
		)
	}
	helper := readRepoFile(t, "scripts", "ci", "unit_deploy.sh")
	for _, required := range []string{
		`PROMETHEUS_URL:-http://127.0.0.1:9095`,
		`python3 "$dir/slo_gate.py" "$@" --deployed-at "$deployed_at" --duration "$duration"`,
		`--max-hold "$max_hold"`,
		`'.gates[$k].duration'`,
		"1) worst=1 ;;",
		`*) [[ "$worst" == 1 ]] || worst=3 ;;`,
	} {
		if !strings.Contains(helper, required) {
			t.Errorf("unit_deploy.sh gate is missing %q", required)
		}
	}
}

// TestEdgeLaneFollowsTheControlPlaneOneHostAtATime: the edge lane deploys
// only after the control-plane lane of the same unit release is on the
// control plane (deploy order), canary first, one host at a time, stopping
// at the first failure, never alongside a full compute rollout of the node.
func TestEdgeLaneFollowsTheControlPlaneOneHostAtATime(t *testing.T) {
	release := readWorkflow(t, "release-units.yml")
	edgeCall := jobBlock(t, release, "deploy-edge")
	for _, required := range []string{"needs: [build, deploy]", "uses: ./.github/workflows/cd-edge-units.yml"} {
		if !strings.Contains(edgeCall, required) {
			t.Errorf("release-units deploy-edge is missing %q", required)
		}
	}
	if !strings.Contains(jobBlock(t, release, "deploy"), "units_lane: control-plane") {
		t.Error("release-units deploy must run the control-plane lane")
	}

	edge := readWorkflow(t, "cd-edge-units.yml")
	orderedIn(t, "cd-edge-units plan", jobBlock(t, edge, "plan"),
		"--lane control-plane --host-role control-plane",
		`if [[ "$action" == deploy ]]; then`,
		"deploy order: the control-plane lane of this unit release",
		"scripts/ci/plan_edge_rollout.py",
		`--canary "${EDGE_CANARY:-}"`,
	)
	if !strings.Contains(edge, "EDGE_CANARY: ${{ inputs.edge_canary || vars.UNIT_RELEASE_EDGE_CANARY }}") {
		t.Error("the edge canary must be configurable by input or environment variable")
	}
	node := readWorkflow(t, "cd-edge-node.yml")
	for _, required := range []string{
		"group: cd-compute-${{ inputs.deploy_environment }}-${{ fromJSON(inputs.target).node }}",
		"--lane edge --host-role compute-only",
		`[[ "$fingerprint" != "$SSH_HOST_KEY_SHA256" ]]`,
		"the edge lane stops",
	} {
		if !strings.Contains(node, required) {
			t.Errorf("cd-edge-node is missing %q", required)
		}
	}
	for _, file := range []string{"cd-edge-units.yml", "cd-edge-node.yml"} {
		if strings.Contains(readWorkflow(t, file), "StrictHostKeyChecking=no") {
			t.Errorf("%s must never disable host-key checking", file)
		}
	}
}

// TestEdgeLaneRunsCanaryFirstWithTheFullGate: the canary node deploys first
// with the full gate; the control plane's gateway and every later node
// follow with the short gate, the control plane only after the canary
// succeeded, the followers one at a time after both, stopping at the first
// failure. A failed follower rolls itself back (cd-edge-node.yml's gate).
func TestEdgeLaneRunsCanaryFirstWithTheFullGate(t *testing.T) {
	edge := readWorkflow(t, "cd-edge-units.yml")
	canary := jobBlock(t, edge, "canary-node")
	for _, required := range []string{
		"needs: [plan]",
		"uses: ./.github/workflows/cd-edge-node.yml",
		"target: ${{ needs.plan.outputs.canary }}",
		"slo_gate: canary",
	} {
		if !strings.Contains(canary, required) {
			t.Errorf("canary-node is missing %q", required)
		}
	}
	cp := jobBlock(t, edge, "control-plane")
	for _, required := range []string{
		"needs: [plan, canary-node]",
		"needs.canary-node.result == 'success' || needs.canary-node.result == 'skipped'",
		"uses: ./.github/workflows/cd-controlplane.yml",
		"units_lane: edge",
		"slo_gate: ${{ needs.plan.outputs.control_plane_gate }}",
	} {
		if !strings.Contains(cp, required) {
			t.Errorf("control-plane is missing %q", required)
		}
	}
	followers := jobBlock(t, edge, "nodes")
	for _, required := range []string{
		"needs: [plan, canary-node, control-plane]",
		"needs.canary-node.result == 'success' || needs.canary-node.result == 'skipped'",
		"needs.control-plane.result == 'success' || needs.control-plane.result == 'skipped'",
		"max-parallel: 1",
		"fail-fast: true",
		"target: ${{ fromJSON(needs.plan.outputs.followers || '[]') }}",
		"uses: ./.github/workflows/cd-edge-node.yml",
		"slo_gate: follower",
	} {
		if !strings.Contains(followers, required) {
			t.Errorf("nodes is missing %q", required)
		}
	}
	// A follower failing its gate rolls itself back and fails the job, which
	// fail-fast turns into the end of the lane.
	orderedIn(t, "cd-edge-node deploy", jobBlock(t, readWorkflow(t, "cd-edge-node.yml"), "deploy"),
		`scripts/ci/unit_deploy.sh gate "$UNIT_PLAN" "$DEPLOYED_AT" "$SLO_GATE" || status=$?`,
		`scripts/ci/unit_deploy.sh rollback "$UNIT_PLAN" "$REMOTE_STAGE"`,
		"the edge lane stops",
		"exit 1",
	)
	// The control-plane lane's single host is its canary.
	if !strings.Contains(readWorkflow(t, "cd-controlplane.yml"), "        default: canary\n") {
		t.Error("cd-controlplane.yml must default to the canary gate")
	}
}

// TestFullAndUnitLanesAreExclusive: exactly one lane runs for a dispatch,
// and each refuses the other's input instead of silently doing nothing.
func TestFullAndUnitLanesAreExclusive(t *testing.T) {
	workflow := readWorkflow(t, "cd-controlplane.yml")
	full := jobBlock(t, workflow, "deploy")
	units := jobBlock(t, workflow, "deploy-units")
	if !strings.Contains(full, "if: github.repository == 'poyrazK/faas' && inputs.units_run_id == ''") {
		t.Error("the full lane must not run for a unit release")
	}
	if !strings.Contains(units, "if: github.repository == 'poyrazK/faas' && inputs.units_run_id != ''") {
		t.Error("the unit lane must run only for a unit release")
	}
	if !strings.Contains(full, "set release_tag (a full release) or units_run_id (a unit release)") {
		t.Error("the full lane must reject a dispatch without a release tag")
	}
	if !strings.Contains(units, "set either release_tag or units_run_id, not both") {
		t.Error("the unit lane must reject a dispatch that also names a release tag")
	}
	if got := strings.Count(workflow, "      units_run_id:\n"); got != 2 {
		t.Errorf("units_run_id is declared %d times, want workflow_call and workflow_dispatch", got)
	}
}
