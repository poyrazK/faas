package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCDPlatformOrchestratesControlComputeAndFleetGate(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-platform.yml"))
	if err != nil {
		t.Fatalf("read cd-platform workflow: %v", err)
	}
	workflow := string(body)
	control := strings.Index(workflow, "uses: ./.github/workflows/cd-controlplane.yml")
	compute := strings.Index(workflow, "uses: ./.github/workflows/cd-compute.yml")
	computeNeedsControl := strings.Index(workflow, "needs: control")
	verify := strings.Index(workflow, "name: Report fleet release convergence")
	gate := strings.Index(workflow, "compute-nodes release-status --desired-release")
	if control < 0 || compute < 0 || computeNeedsControl < 0 || verify < 0 || gate < 0 {
		t.Fatalf("platform workflow is incomplete: control=%d compute=%d needs=%d verify=%d gate=%d", control, compute, computeNeedsControl, verify, gate)
	}
	if !(control < computeNeedsControl && computeNeedsControl < compute && compute < verify && verify < gate) {
		t.Fatalf("platform rollout stages are out of order: control=%d needs=%d compute=%d verify=%d gate=%d", control, computeNeedsControl, compute, verify, gate)
	}
	if !strings.Contains(workflow, "--timeout '${gate_timeout}'") || !strings.Contains(workflow, "--break-glass-db --json") {
		t.Fatal("platform workflow must execute the bounded, machine-readable active-node release gate")
	}
	if strings.Contains(workflow, "--property=EnvironmentFile=/etc/faas/compute-db.env") {
		t.Fatal("control-plane release gate must let gregalectl fall back to sealed.env when compute-db.env is absent")
	}
}

// This pins the incident from #2348: a successful control-plane stage followed
// by a failed compute stage still runs the observer, records desired/observed
// node releases, and leaves the top-level workflow failed.
func TestCDPlatformControlSuccessThenComputeFailureIsVisible(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-platform.yml"))
	if err != nil {
		t.Fatalf("read cd-platform workflow: %v", err)
	}
	workflow := string(body)
	for _, required := range []string{
		"if: always()",
		"needs: [control, compute]",
		"CONTROL_RESULT: ${{ needs.control.result }}",
		"COMPUTE_RESULT: ${{ needs.compute.result }}",
		`if [[ "$CONTROL_RESULT" != "success" || "$COMPUTE_RESULT" != "success" ]]; then`,
		`if [[ "$CONTROL_RESULT" != "success" || "$COMPUTE_RESULT" != "success" || "$gate_exit" -ne 0 ]]; then`,
		"active-node-gate=${gate_exit}",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("platform workflow does not preserve compute-stage failure visibility; missing %q", required)
		}
	}
	if got := strings.Count(workflow, "gate_timeout=0"); got != 1 {
		t.Fatalf("failed stage must select exactly one immediate observation path; got %d", got)
	}
}

func TestCDPlatformRequiresPublicReadinessAndProductionDeployments(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-platform.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	releaseGate := strings.Index(workflow, "compute-nodes release-status --desired-release")
	publicReadiness := strings.Index(workflow, "https://api.gregale.dev/${endpoint}")
	metricsGate := strings.Index(workflow, "Verify full-fleet metrics and metering convergence")
	deployGate := strings.Index(workflow, "production-release-acceptance.sh")
	if releaseGate < 0 || publicReadiness < 0 || metricsGate < 0 || deployGate < 0 {
		t.Fatalf("production gates missing: release=%d readiness=%d metrics=%d deploy=%d", releaseGate, publicReadiness, metricsGate, deployGate)
	}
	if !(releaseGate < publicReadiness && publicReadiness < metricsGate && metricsGate < deployGate) {
		t.Fatalf("production gates out of order: release=%d readiness=%d metrics=%d deploy=%d", releaseGate, publicReadiness, metricsGate, deployGate)
	}
	for _, required := range []string{"ACTIVE_NODE_COUNT", "healthz readyz", "RELEASE_SHA='${DESIRED_RELEASE}'"} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("production deployment gate missing %q", required)
		}
	}
}

func TestNormalAnsibleConvergenceRemovesLegacyOCIOverrides(t *testing.T) {
	for _, role := range []string{"control_plane_service", "compute_only_service"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", role, "tasks", "main.yml"))
		if err != nil {
			t.Fatal(err)
		}
		tasks := string(body)
		for _, required := range []string{"remove legacy OCI E2E storage overrides", "/etc/faas/oci-e2e.env", "/etc/systemd/system/faas-schedd.service.d/99-oci-e2e.conf", "notify: restart faas-schedd"} {
			if !strings.Contains(tasks, required) {
				t.Fatalf("%s role does not remove legacy storage override %q", role, required)
			}
		}
	}
	controlBody, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "control_plane_service", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"/etc/systemd/system/faas-apid.service.d/99-codex-oci-source.conf",
		"/etc/systemd/system/faas-apid.service.d/99-oci-e2e.conf",
		"restart faas-apid",
	} {
		if !strings.Contains(string(controlBody), required) {
			t.Fatalf("control_plane_service role does not remove legacy apid storage override %q", required)
		}
	}
}

func TestCDStageWorkflowsAreReusableByPlatformRollout(t *testing.T) {
	for _, name := range []string{"cd-controlplane.yml", "cd-compute.yml"} {
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		workflow := string(body)
		if !strings.Contains(workflow, "  workflow_call:\n") || !strings.Contains(workflow, "      release_tag:\n") {
			t.Fatalf("%s cannot be called by cd-platform", name)
		}
	}
}

func TestDeployVersionSkewDegradesDuringMixedRelease(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml"))
	if err != nil {
		t.Fatalf("read faas rules: %v", err)
	}
	workflow := string(body)
	start := strings.Index(workflow, "- alert: FaasDeployVersionSkew")
	if start < 0 {
		t.Fatal("FaasDeployVersionSkew alert is missing")
	}
	end := strings.Index(workflow[start:], "\n      - alert:")
	if end < 0 {
		t.Fatal("cannot isolate FaasDeployVersionSkew rule")
	}
	rule := workflow[start : start+end]
	if strings.Contains(rule, "\n        for:") {
		t.Fatal("version skew remains hidden behind a hold duration")
	}
	if !strings.Contains(rule, `count(count by (version) ({__name__=~".*_faas_deploy_version"})) > 1`) {
		t.Fatal("version skew alert no longer detects multiple observed releases")
	}
}
