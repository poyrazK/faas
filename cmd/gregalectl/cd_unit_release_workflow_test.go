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

// TestUnitReleaseIsSignedOnlyByMainAndVerifiedBeforeUse: the unit lane
// installs binaries as root on the control plane. They must come from
// release-units.yml on main, match the signed manifest file by file, and be
// verified before anything reaches the host.
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

	units := jobBlock(t, readWorkflow(t, "cd-controlplane.yml"), "deploy-units")
	verify := strings.Index(units, `--certificate-identity "https://github.com/poyrazK/faas/.github/workflows/release-units.yml@refs/heads/main"`)
	digests := strings.Index(units, `(cd "$dir/unpacked" && sha256sum -c -)`)
	count := strings.Index(units, `-eq "$(jq '.units | length' "$manifest")"`)
	ssh := strings.Index(units, "ssh -i ~/.ssh/deploy_key")
	if verify < 0 || digests < 0 || count < 0 || ssh < 0 {
		t.Fatalf("deploy-units lacks verification steps: verify=%d digests=%d count=%d ssh=%d", verify, digests, count, ssh)
	}
	if !(verify < digests && digests < count && count < ssh) {
		t.Fatalf("deploy-units must verify the signature, each binary and the binary count before contacting the host")
	}
}

// TestUnitLaneRestartsOnlyPlannedServicesOverTheExpectedRelease: the host
// step must compare-and-swap against the release the plan was made over,
// restart only planned services, gate each one, and roll back on a failed
// gate, all under the customer-path observer the full lane uses.
func TestUnitLaneRestartsOnlyPlannedServicesOverTheExpectedRelease(t *testing.T) {
	units := jobBlock(t, readWorkflow(t, "cd-controlplane.yml"), "deploy-units")
	steps := []string{
		"scripts/ci/plan_unit_deploy.py",
		"deployctl unit-overlay --release-id ${release_id} --expect-current ${expect}",
		"deployctl deploy --only '${only}' --expect-current ${expect} ${release_id}",
		`touch \"\$ROLLOUT_ACTIVATION_MARKER\"`,
		"unit-release-verify.sh ${release_dir}${specs}",
		"deployctl rollback --only '${only}'",
		"scripts/ci/observe_remote_rollout.sh",
	}
	last := -1
	for _, step := range steps {
		at := strings.Index(units, step)
		if at < 0 {
			t.Fatalf("deploy-units is missing %q", step)
		}
		if at < last {
			t.Fatalf("deploy-units runs %q out of order", step)
		}
		last = at
	}
	for _, forbidden := range []string{"migrate", "converge-control-plane", "kgv rotate", "fleet-seal"} {
		if strings.Contains(units, forbidden) {
			t.Errorf("the unit lane must leave %q to the full lane", forbidden)
		}
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
