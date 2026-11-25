//go:build metal

// vmmd_unit_lane_metal_test.go — the compute unit lane's vmmd adoption proof
// (docs/adr/20261123122457-compute-unit-lane.md, decision 3) against a live
// vmmd with a running guest. The proof is the shell scripts/ci/unit_deploy.sh
// writes into a node's activation script, run as generated: only the service
// manager (systemctl), the staged candidate and its config are pointed at the
// harness. It must stand down without touching the node when the serving vmmd
// does not hand its guests off, pass when the candidate adopts every live
// guest, and the restarted vmmd's adoption report must then come back clean
// with the guest's Firecracker process kept.

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// unitLaneStage is the stage directory the generated script names; the
// test points it at the harness's binaries.
const unitLaneStage = "/var/tmp/gregale-units.LaneTest"

func TestVMMDUnitLaneAdoptionProofMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal unit-lane adoption test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if e2etest.VMMDUnderSystemd() {
		t.Skip("the proof reads the serving vmmd's environment by PID; run without the systemd wrapper")
	}
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")
	e2etest.OverrideVMMDConfig(t, "adoption_record = true\nadopt_running_vms = true\n")

	// Production sets the adoption switches through the unit's environment
	// (99-faas-adoption.conf); the proof reads them from the serving process.
	h := e2etest.Start(t, pool, e2etest.DeployWake,
		"FAAS_VMMD_ADOPTION_RECORD=true", "FAAS_VMMD_ADOPT_RUNNING_VMS=true")
	defer h.DumpLogs(t)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, raw)
	}
	ctx := context.Background()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 15*time.Minute); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 5*time.Minute); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}
	url, host := gatewayAppURL(h, "hello"), "hello.apps.test.example"
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, url, 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	if body, status := doGetWithHost(t, client, url, host, 30*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("first wake: status=%d body=%q", status, body)
	}
	running, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second)
	if err != nil {
		t.Fatalf("no running instance after wake: %v", err)
	}
	guest := running[0]
	guestPID := firecrackerPID(t, guest.ID)
	vmmdPID, err := h.VMMDPID()
	if guestPID == 0 || err != nil {
		t.Fatalf("running guest %s: firecracker pid %d, vmmd %v", guest.ID, guestPID, err)
	}

	proof := unitLaneProof(t, h)

	// 1. The serving vmmd does not hand off (its environment lacks
	//    adopt_running_vms; here: the test process stands in for it): the
	//    proof stands down with 3 and the node is untouched.
	out, code := proof.run(t, os.Getpid())
	if code != 3 || !strings.Contains(out, "does not hand its guests off") {
		t.Fatalf("a serving vmmd without the hand-off: exit %d, want 3; output:\n%s", code, out)
	}
	if got, _ := h.VMMDPID(); got != vmmdPID || firecrackerPID(t, guest.ID) != guestPID {
		t.Fatalf("a stand-down changed the node: vmmd %d->%d, guest %d->%d", vmmdPID, got, guestPID, firecrackerPID(t, guest.ID))
	}

	// 2. The candidate adopts every live guest: the proof passes and names
	//    the guests already gone (none here).
	out, code = proof.run(t, vmmdPID)
	if code != 0 {
		t.Fatalf("adoption proof against a serving vmmd with one live guest: exit %d; output:\n%s", code, out)
	}
	var check struct {
		AllAdoptable bool `json:"all_adoptable"`
		Adoptable    int  `json:"adoptable"`
	}
	decoded := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &check) == nil {
			decoded = true
		}
	}
	if !decoded || !check.AllAdoptable || check.Adoptable < 1 {
		t.Fatalf("adoption-check did not judge the live guest %s adoptable (%+v):\n%s", guest.ID, check, out)
	}
	gone := lastLine(out)
	if !strings.HasPrefix(gone, "gone=") {
		t.Fatalf("proof did not print the gone guests: %q", gone)
	}

	// 3. The restart (deployctl's planned stop and start) and the report the
	//    activation script requires before it keeps the node.
	if err := h.PlannedRestartVMMD(30 * time.Second); err != nil {
		t.Fatalf("restart vmmd: %v", err)
	}
	report := exec.Command(filepath.Join(h.BinDir, "vmmd"), "adoption-report", "--wait", "90s",
		"--already-gone", strings.TrimPrefix(gone, "gone="))
	var reportOut bytes.Buffer
	report.Stdout, report.Stderr = &reportOut, &reportOut
	if err := report.Run(); err != nil {
		t.Fatalf("adoption-report after the restart: %v\n%s", err, reportOut.String())
	}
	t.Logf("adoption-report: %s", strings.TrimSpace(reportOut.String()))
	if got := firecrackerPID(t, guest.ID); got != guestPID {
		t.Fatalf("firecracker pid after the restart = %d, want the adopted %d", got, guestPID)
	}
	if after := instanceByID(t, pool, guest.ID); after.State != string(state.StateRunning) {
		t.Fatalf("adopted instance is %s after the restart", after.State)
	}
	if body, status := doGetWithHost(t, client, url, host, 30*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("adopted guest after the restart: status=%d body=%q", status, body)
	}

	// Park, so no VM outlives the harness (adoption keeps guests through a
	// vmmd stop).
	setAppIdleTimeout(t, h, key, "hello", api.IdleTimeoutFloorSeconds)
	if _, err := e2etest.WaitForAppParked(ctx, t, pool, appID, 90*time.Second); err != nil {
		t.Fatalf("adopted instance did not park: %v", err)
	}
	waitJailRemoved(t, guest.ID, 60*time.Second)
}

// laneProof is the adoption proof block of a generated activation script,
// with the service manager stubbed to report a chosen MainPID.
type laneProof struct {
	script string
	stub   string
}

func unitLaneProof(t *testing.T, h *e2etest.Harness) laneProof {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plan := filepath.Join(dir, "plan.json")
	body := `{"action": "deploy",
  "release_id": "0123456789abcdef0123456789abcdef01234567",
  "expect_current": "00000000000000000000000000000000000000b0",
  "commit": "1111111111111111111111111111111111111111",
  "units": ["vmmd"], "only": ["vmmd"], "verify": ["vmmd=http://127.0.0.1:9104/readyz"],
  "gate": [["--unit", "vmmd"]],
  "replace": {"bin/vmmd": "bin/vmmd", "bin/deployctl": "bin/deployctl", "unit-versions.json": "unit-versions.json"}}`
	if err := os.WriteFile(plan, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(dir, "activate.sh")
	cmd := exec.Command("bash", filepath.Join(repo, "scripts", "ci", "unit_deploy.sh"), "script", plan, unitLaneStage, generated)
	cmd.Env = append(os.Environ(), "UNIT_SSH_TARGET=root@node.invalid", "RUNNER_TEMP="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate the activation script: %v\n%s", err, out)
	}
	text, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	var block []string
	in := false
	for _, line := range strings.Split(string(text), "\n") {
		if strings.HasPrefix(line, "stand_down()") {
			in = true
		}
		if in {
			block = append(block, line)
		}
		if in && strings.HasPrefix(line, "adoption_gone=") {
			break
		}
	}
	if len(block) == 0 || !strings.HasPrefix(block[len(block)-1], "adoption_gone=") {
		t.Fatalf("the generated activation script has no adoption proof:\n%s", text)
	}
	src := strings.Join(block, "\n")
	for old, repl := range map[string]string{
		unitLaneStage + "/bin/vmmd": filepath.Join(h.BinDir, "vmmd"),
		"/etc/faas/vmmd.toml":       h.VMMDConfigPath(),
		"sleep 10":                  "sleep 1",
	} {
		if !strings.Contains(src, old) {
			t.Fatalf("the adoption proof no longer names %q:\n%s", old, src)
		}
		src = strings.ReplaceAll(src, old, repl)
	}
	script := filepath.Join(dir, "proof.sh")
	if err := os.WriteFile(script, []byte(src+"\necho \"gone=$adoption_gone\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(dir, "bin")
	if err := os.Mkdir(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, "systemctl"), []byte("#!/usr/bin/env bash\necho \"$PROOF_MAINPID\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return laneProof{script: script, stub: stub}
}

func (p laneProof) run(t *testing.T, mainPID int) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "set -euo pipefail; source "+p.script)
	cmd.Env = append(os.Environ(), "PATH="+p.stub+":"+os.Getenv("PATH"), "PROOF_MAINPID="+strconv.Itoa(mainPID))
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(out), exitErr.ExitCode()
		}
		t.Fatalf("run the adoption proof: %v", err)
	}
	return string(out), 0
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
