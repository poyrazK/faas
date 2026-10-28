// normal_path_debug_status_e2e_test.go — KVM-free acceptance for the
// debugger's starting points (ADR-1447 amendment, ADR-121): `debug status`
// and `debug rollout` read what the real processes recorded. The gateway
// records every request, schedd records the out-of-memory kill vmmd reports,
// and apid and the CLI explain both. The rollback decision itself is written
// at the state boundary, as the orchestrator would, because the canary ladder
// runs in meterd, which this harness does not start; the metal safe-release
// drill covers the decision.

//go:build !no_pg

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

const debugStatusSlug = "normal-debug-status"

// TestE2E_NormalPath_DebugStatusExplainsRollbackAndOOM walks the three
// answers `gregale debug status` gives: a healthy app, a canary rolled back
// for server errors (with the candidate's own failing requests as examples),
// and an out-of-memory kill that leaves the live release serving.
func TestE2E_NormalPath_DebugStatusExplainsRollbackAndOOM(t *testing.T) {
	f := newNormalPathDebuggerFixture(t, debugStatusSlug)
	if f == nil {
		return
	}
	stable, stableInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "status-stable")
	f.vmmd.SetVersion(stableInstance.ID, "status-stable")
	f.vmmd.SetResponseForPath(stableInstance.ID, "/checkout", e2etest.FakeResponse{Status: http.StatusOK, Body: []byte("checkout:stable")})
	waitForNormalPathDebuggerResponse(t, f, "normal-path:status-stable\n", 10*time.Second)
	gregale := buildGregale(t)

	// Healthy: one serving deployment, its requests, nothing to explain.
	for range 3 {
		if status, body := debugStatusGet(t, f, "/checkout"); status != http.StatusOK {
			t.Fatalf("stable GET /checkout = %d %q", status, body)
		}
	}
	healthy := waitForDebugStatus(t, f, "3 stable requests", func(s api.DebugStatusResponse) bool { return s.Health.Requests >= 4 })
	if healthy.Verdict != api.DebugStatusHealthy || len(healthy.Deployments) != 1 || healthy.Deployments[0].ID != stable.ID ||
		healthy.Health.ServerErrors != 0 || healthy.Rollout != nil || healthy.OOMKillsTotal != 0 {
		t.Fatalf("healthy status = %+v", healthy)
	}
	if out, errOut, exit := runGregaleAgainstHarness(t, gregale, f, "debug", "status", debugStatusSlug); exit != 0 || !strings.HasPrefix(out, debugStatusSlug+": healthy") {
		t.Fatalf("gregale debug status (healthy): exit=%d stdout=%q stderr=%q", exit, out, errOut)
	}

	// A canary at 50% answers /checkout with 500s; the gateway alternates
	// deployments request by request.
	// The gateway stamps collapsed telemetry rows with their minute, and the
	// orchestrator's decision windows are whole minutes, so the window starts
	// on one.
	windowStart := time.Now().UTC().Truncate(time.Minute)
	candidate := startDebugStatusCanary(t, f, stable.ID, stableInstance.ID)
	failed, served := 0, 0
	for range 12 {
		switch status, _ := debugStatusGet(t, f, "/checkout"); status {
		case http.StatusInternalServerError:
			failed++
		case http.StatusOK:
			served++
		}
	}
	if failed == 0 || served == 0 {
		t.Fatalf("canary split: %d failed, %d served; want both deployments reached", failed, served)
	}
	waitForDebugCandidateFailures(t, f, candidate.ID, failed)
	abortDebugStatusCanary(t, f, stable.ID, candidate.ID, windowStart)

	rollout := debugStatusAPI[api.DebugRolloutExplanation](t, f, "/v1/apps/"+debugStatusSlug+"/debug/rollout")
	if rollout.State != api.DebugRolloutAborted || rollout.DeploymentID != candidate.ID || rollout.StableDeploymentID != stable.ID ||
		rollout.Decision == nil || rollout.Decision.Rule != api.RolloutRuleAbort5xx {
		t.Fatalf("rollout = %+v", rollout)
	}
	if len(rollout.Routes) == 0 || rollout.Routes[0].Route != "/checkout" || rollout.Routes[0].CandidateServerErrors != int64(failed) ||
		rollout.Routes[0].CandidateRequests != int64(failed) || rollout.Routes[0].StableServerErrors != 0 || rollout.Routes[0].StableRequests < int64(served) {
		t.Fatalf("rollout routes = %+v; want /checkout with the candidate's %d failures against %d stable successes", rollout.Routes, failed, served)
	}
	if !strings.HasPrefix(rollout.Headline, "Rolled back at 50% traffic") || !strings.Contains(rollout.Headline, "Worst route /checkout") {
		t.Fatalf("rollout headline = %q", rollout.Headline)
	}
	if len(rollout.Examples) == 0 || len(rollout.Examples) > api.DebugRolloutMaxExamples {
		t.Fatalf("rollout examples = %+v", rollout.Examples)
	}
	for _, example := range rollout.Examples {
		if example.Status != http.StatusInternalServerError || example.DeploymentID != candidate.ID || example.Route != "/checkout" {
			t.Fatalf("rollout example = %+v; want one of the candidate's failed /checkout requests", example)
		}
	}
	// Each example opens its request evidence.
	evidence := debugStatusAPI[api.DebugRequestEvidenceResponse](t, f, "/v1/apps/"+debugStatusSlug+"/debug/requests/"+rollout.Examples[0].ID+"/evidence")
	if evidence.Request.ID != rollout.Examples[0].ID || evidence.Request.Status != http.StatusInternalServerError {
		t.Fatalf("example evidence = %+v", evidence.Request)
	}
	rolledBack := debugStatusAPI[api.DebugStatusResponse](t, f, "/v1/apps/"+debugStatusSlug+"/debug/status")
	if rolledBack.Verdict != api.DebugStatusRollback || rolledBack.Rollout == nil || rolledBack.Rollout.DeploymentID != candidate.ID ||
		rolledBack.Headline != rollout.Headline || len(rolledBack.Deployments) != 1 || rolledBack.Deployments[0].ID != stable.ID {
		t.Fatalf("status after the rollback = %+v", rolledBack)
	}
	// `debug explain` is the command tree's name for `debug rollout`.
	out, errOut, exit := runGregaleAgainstHarness(t, gregale, f, "debug", "explain", debugStatusSlug)
	if exit != 0 || !strings.HasPrefix(out, rollout.Headline) || !strings.Contains(out, "Decision abort_5xx (abort)") ||
		!strings.Contains(out, "gregale debug requests inspect "+debugStatusSlug+" ") {
		t.Fatalf("gregale debug explain: exit=%d stdout=%q stderr=%q", exit, out, errOut)
	}

	// vmmd reports the stable instance's out-of-memory kill. The release is
	// live, so it keeps serving (ADR-121 amendment): the kill is recorded,
	// the deployment is not failed.
	reportDebugStatusOOM(t, f, stableInstance.ID, f.app.RAMMB+40, f.app.RAMMB)
	oom := waitForDebugStatus(t, f, "the OOM kill", func(s api.DebugStatusResponse) bool { return s.OOMKillsTotal > 0 })
	if oom.OOMKillsTotal != 1 || len(oom.OOMKills) != 1 {
		t.Fatalf("OOM kills = %d %+v", oom.OOMKillsTotal, oom.OOMKills)
	}
	kill := oom.OOMKills[0]
	if kill.DeploymentID != stable.ID || kill.PeakMB != f.app.RAMMB+40 || kill.PlanMB != f.app.RAMMB || kill.DeploymentFailed || !strings.Contains(oom.OOMFix, "MB") {
		t.Fatalf("OOM kill = %+v fix %q; want the stable instance's kill, deployment kept", kill, oom.OOMFix)
	}
	if dep, err := f.store.DeploymentByID(f.ctx, stable.ID); err != nil || dep.Status != state.DeployLive {
		t.Fatalf("stable deployment after its instance's OOM kill: status=%q err=%v, want live", dep.Status, err)
	}
	// The rollback still ranks first; the kill is listed with it.
	if oom.Verdict != api.DebugStatusRollback {
		t.Fatalf("verdict with a rollback and an OOM kill = %q, want %q", oom.Verdict, api.DebugStatusRollback)
	}
	if out, errOut, exit := runGregaleAgainstHarness(t, gregale, f, "debug", "status", debugStatusSlug); exit != 0 || !strings.Contains(out, "(instance replaced)") || !strings.Contains(out, "Memory:") {
		t.Fatalf("gregale debug status (OOM): exit=%d stdout=%q stderr=%q", exit, out, errOut)
	}
}

func debugStatusGet(t *testing.T, f *normalPathFixture, path string) (int, []byte) {
	t.Helper()
	_, body, status := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + f.key})
	return status, body
}

func debugStatusAPI[T any](t *testing.T, f *normalPathFixture, path string) T {
	t.Helper()
	var out T
	body, status := doReq(t, f.h, f.key, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, status, body)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v body=%s", path, err, body)
	}
	return out
}

func waitForDebugStatus(t *testing.T, f *normalPathFixture, what string, ready func(api.DebugStatusResponse) bool) api.DebugStatusResponse {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status := debugStatusAPI[api.DebugStatusResponse](t, f, "/v1/apps/"+debugStatusSlug+"/debug/status")
		if ready(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("debug status did not show %s within 20s: %+v", what, status)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitForDebugCandidateFailures waits until the gateway's records of the
// candidate's failed requests reached apid.
func waitForDebugCandidateFailures(t *testing.T, f *normalPathFixture, candidateID string, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		page := debugStatusAPI[api.DebugTelemetryListResponse](t, f, "/v1/apps/"+debugStatusSlug+"/debug/requests?since=1h&limit=200")
		got := 0
		for _, request := range page.Requests {
			if request.DeploymentID == candidateID && request.Status == http.StatusInternalServerError {
				got += max(request.Count, 1) // a collapsed row represents several requests
			}
		}
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of the candidate's %d failed requests reached the debugger within 20s", got, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// startDebugStatusCanary puts a candidate at the 50% step of a four-step
// canary beside the stable deployment and waits until the gateway reaches
// both.
func startDebugStatusCanary(t *testing.T, f *normalPathFixture, stableID, stableInstanceID string) state.Deployment {
	t.Helper()
	candidate, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID: f.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64),
		CanaryPreset: "balanced", CanaryStep: 2, CanaryTotalSteps: 4, TrafficPercent: 50,
	})
	if err != nil {
		t.Fatalf("create canary: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, candidate.ID); err != nil {
		t.Fatalf("mark canary live: %v", err)
	}
	publishNormalPathLayer(t, f, candidate.ID)
	instance, err := f.store.CreateInstance(f.ctx, f.app.ID, candidate.ID, string(state.StateRunning), f.app.RAMMB, f.nodeID, "")
	if err != nil {
		t.Fatalf("create canary instance: %v", err)
	}
	f.vmmd.SetVersion(instance.ID, "status-candidate")
	f.vmmd.SetResponseForPath(instance.ID, "/checkout", e2etest.FakeResponse{Status: http.StatusInternalServerError, Body: []byte("checkout:candidate")})
	notifyDebugStatusDeployment(t, f, candidate.ID, instance.ID)
	if got, err := f.store.DeploymentByID(f.ctx, candidate.ID); err != nil || got.RolloutState != "rolling_out" || got.TrafficPercent != 50 {
		t.Fatalf("canary = %+v err=%v, want rolling_out at 50%%", got, err)
	}
	seen := map[string]bool{}
	deadline := time.Now().Add(30 * time.Second)
	for !seen["normal-path:status-stable\n"] || !seen["normal-path:status-candidate\n"] {
		if time.Now().After(deadline) {
			t.Fatalf("GET / reached %v within 30s, want both deployments", seen)
		}
		if status, body := debugStatusGet(t, f, "/"); status == http.StatusOK {
			seen[string(body)] = true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return candidate
}

func notifyDebugStatusDeployment(t *testing.T, f *normalPathFixture, deploymentID, instanceID string) {
	t.Helper()
	deployment, _ := json.Marshal(map[string]string{"app_id": f.app.ID, "deployment_id": deploymentID, "status": string(state.DeployLive)})
	instance, _ := json.Marshal(map[string]string{"app_id": f.app.ID, "instance_id": instanceID, "state": string(state.StateRunning)})
	if err := db.Notify(f.ctx, f.h.Pool, db.NotifyDeploymentChanged, string(deployment)); err != nil {
		t.Fatalf("notify deployment_changed: %v", err)
	}
	if err := db.Notify(f.ctx, f.h.Pool, db.NotifyInstanceChanged, string(instance)); err != nil {
		t.Fatalf("notify instance_changed: %v", err)
	}
}

// abortDebugStatusCanary records the rollback the safe-release orchestrator
// makes for a 5xx regression (ADR-1447): the candidate at 0% with its
// decision, the stable deployment back at 100%.
func abortDebugStatusCanary(t *testing.T, f *normalPathFixture, stableID, candidateID string, windowStart time.Time) {
	t.Helper()
	now := time.Now().UTC()
	decision, err := json.Marshal(api.RolloutDecision{
		Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbort5xx, Summary: "5xx regression",
		Step: 2, TrafficPercent: 50, WindowStart: windowStart.Format(time.RFC3339Nano), WindowEnd: now.Format(time.RFC3339Nano),
		StableDeploymentID: stableID, DecidedAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.Pool.Exec(f.ctx, `UPDATE deployments
		   SET traffic_percent = 0, rollout_state = 'aborted', rollout_aborted_at = $2,
		       rollout_aborted_reason = '5xx error rate regression', rollout_decision = $3::jsonb
		 WHERE id = $1`, candidateID, now, string(decision)); err != nil {
		t.Fatalf("record the rollback: %v", err)
	}
	if _, err := f.h.Pool.Exec(f.ctx, `UPDATE deployments SET traffic_percent = 100 WHERE id = $1`, stableID); err != nil {
		t.Fatalf("restore the stable deployment's traffic: %v", err)
	}
}

// reportDebugStatusOOM is vmmd's report of a workload OOM kill (ADR-121).
func reportDebugStatusOOM(t *testing.T, f *normalPathFixture, instanceID string, peakMB, planMB int) {
	t.Helper()
	conn, err := grpc.NewClient("unix://"+f.h.ScheddSock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial schedd: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	ack, err := scheddpb.NewScheddClient(conn).ReportWorkloadOOM(ctx, &scheddpb.ReportWorkloadOOMRequest{
		InstanceId: instanceID, PeakMb: uint32(peakMB), PlanMb: uint32(planMB), SourceNodeId: f.nodeID, //nolint:gosec // test plan sizes fit
	})
	if err != nil || !ack.GetOk() {
		t.Fatalf("report workload OOM: ack=%+v err=%v", ack, err)
	}
}
