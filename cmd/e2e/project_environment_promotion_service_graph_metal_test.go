//go:build promotionacceptance && metal

package e2e_test

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reuse six real native builds across three independent public admissions.
// Faults affect owned processes/guest behaviour, never controller evidence.
func TestProjectEnvironmentPromotionServiceGraphMetal(t *testing.T) {
	p := newPromotionServiceGraphNative(t)
	foreign := newPromotionGraphForeignCaller(t, p)
	fault := newPromotionNativeProbeFault(t, p)
	fault.delayAfter.Store(true)
	cases := map[string]any{}
	for _, name := range []string{"preflight_refusal", "rollback", "success"} {
		p.refreshControlBaseline(t)
		cases[name] = p.qualifyServiceGraph(t, fault, foreign, name)
	}
	for _, app := range foreign.apps {
		mustProjectWorkerNative(t, foreign.owner.DeleteApp(t.Context(), app.Slug))
	}
	foreign.cleanupDone = true
	p.cleanup(t)
	if !t.Failed() {
		logPromotionNativeEvidence(t, map[string]any{"schema_version": 1, "scenario": "service_graph", "cases": cases, "cleanup": true})
	}
}

func (p *environmentPromotionNative) admitGraphPromotion(t *testing.T) (string, string) {
	t.Helper()
	for _, app := range p.apps {
		var count int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE app_id=$1", app.ID).Scan(&count))
		p.counts[app.ID] = count
	}
	preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "production", "staging", true, true, false, false)
	mustProjectWorkerNative(t, err)
	if !preview.CanPromote || preview.Health == nil || len(preview.Health.Probes) != 2 || preview.DestinationConfigHash == "" || preview.DestinationConfigHash == p.sourceConfig.ConfigHash {
		t.Fatalf("service graph admission missing destination qualification: %v", preview.BlockingReasons)
	}
	accepted, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken}, uuid.NewString())
	mustProjectWorkerNative(t, err)
	if accepted.Status != "running" || accepted.PromotionID == "" {
		t.Fatal("service graph promotion was not durably admitted")
	}
	return accepted.PromotionID, preview.DestinationConfigHash
}

func (p *environmentPromotionNative) qualifyServiceGraph(t *testing.T, fault *promotionNativeProbeFault, foreign *projectWorkerNativeFixture, name string) map[string]any {
	t.Helper()
	baseline := p.baseline.ID
	before := p.publicationCount(t)
	fault.delayed.Store(true)
	id, config := p.admitGraphPromotion(t)
	select {
	case <-fault.started:
	case <-time.After(2 * time.Minute):
		t.Fatal("no real frontend dependency probe")
	}
	held := p.status(t, id)
	if held.Health.Phase != "preflight" {
		t.Fatal("candidate escaped dark preflight")
	}
	mustProjectWorkerNative(t, p.h.KillAPID())
	p.apidStopped = true
	fault.delayed.Store(false)
	deployments := map[string]string{}
	for _, w := range held.Workloads {
		deployments[w.WorkloadName] = w.TargetDeploymentID
	}
	if deployments["frontend"] == "" || deployments["backend"] == "" {
		t.Fatal("incomplete candidate graph")
	}
	frontend := p.graphInstance(t, p.apps["frontend"].ID, deployments["frontend"])
	backend := p.graphInstance(t, p.apps["backend"].ID, deployments["backend"])
	initial := p.graphReport(t, frontend, backend.DeploymentID, id, "tested", "https")
	p.assertGraph(t, baseline)
	p.assertConfigOffline(t, p.targetConfig.ConfigHash)
	p.assertHTTP(t, "baseline", "baseline-config")
	proof := map[string]any{"promotion_id": id, "baseline_graph": baseline, "private_graph": id, "source_config_hash": p.sourceConfig.ConfigHash, "destination_config_hash": config, "baseline_config_hash": p.targetConfig.ConfigHash, "frontend_deployment": frontend.DeploymentID, "backend_deployment": backend.DeploymentID, "initial_frontend_instance": frontend.ID, "initial_backend_instance": backend.ID, "private_report": initial, "private_routing": true, "destination_secrets": true, "public_baseline": true}
	if name == "success" {
		proof["isolation"] = p.qualifyGraphIsolation(t, frontend, backend, id, foreign)
		var snapshots int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM snapshots WHERE deployment_id=$1 AND NOT stale", backend.DeploymentID).Scan(&snapshots))
		if snapshots != 0 {
			t.Fatal("cold recovery requires a fresh candidate without snapshots")
		}
		faultedAt := time.Now().UTC()
		p.killGraphVM(t, backend, p.apps["backend"].ID)
		mustProjectWorkerNative(t, p.h.RestartVMMD())
		mustProjectWorkerNative(t, p.h.RestartGateway())
		mustProjectWorkerNative(t, p.h.RestartSchedd())
		// A real request from the candidate frontend wakes its exact backend.
		var recovered projectWorkerNativeReport
		waitProjectWorkerNative(t, 2*time.Minute, "candidate graph cold recovery", func() bool {
			body, status := graphGuestRequest(t, frontend, "/", http.MethodGet)
			if status != http.StatusOK {
				return false
			}
			recovered = decodeGraphReport(t, body)
			return recovered.BackendInstanceID != "" && recovered.BackendInstanceID != backend.ID
		})
		recovered = p.graphReport(t, frontend, backend.DeploymentID, id, "tested", "https")
		var cold bool
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM events e JOIN instances i ON i.id::text=e.data->>'instance_id'
WHERE i.id=$1 AND i.deployment_id=$2 AND e.actor='schedd' AND e.kind='wake.boot_started' AND e.data->>'method'='cold_boot' AND e.at >= $3)`, recovered.BackendInstanceID, backend.DeploymentID, faultedAt).Scan(&cold))
		if !cold {
			t.Fatal("replacement API lacked a daemon-owned cold-boot event")
		}
		proof["cold_boot_event"] = true
		proof["recovered_report"], proof["cold_without_snapshot"], proof["daemon_restarts"] = recovered, true, []string{"apid", "vmmd", "gatewayd-internal", "schedd"}
	}
	if name == "preflight_refusal" {
		p.regressGraphBackend(t, backend, frontend)
	}
	checkpoint, err := p.store.ProjectEnvironmentPromotionHealth(t.Context(), p.accountID, id)
	mustProjectWorkerNative(t, err)
	if checkpoint.Phase != "preflight" || checkpoint.Generation != held.Health.Generation || !checkpoint.Deadline.Equal(held.Health.Deadline) {
		t.Fatal("daemon faults reset the preflight checkpoint")
	}
	proof["checkpoint_preserved"] = true
	proof["preflight_generation"], proof["resumed_generation"] = held.Health.Generation, checkpoint.Generation
	proof["preflight_deadline"], proof["resumed_deadline"] = held.Health.Deadline, checkpoint.Deadline
	mustProjectWorkerNative(t, p.h.RestartAPID())
	p.apidStopped = false
	preflightRounds, regressions := 0, 0
	var final api.ProjectEnvironmentPromotionStatusResponse
	var cutover string
	var windows []time.Time
	regressed := name == "preflight_refusal"
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+2*time.Minute, "service graph promotion outcome", func() bool {
		final = p.status(t, id)
		h := final.Health
		if h.Phase == "preflight" || h.Phase == "cutover" {
			preflightRounds = max(preflightRounds, h.HealthyRounds)
			p.assertGraph(t, baseline)
		}
		regressions = max(regressions, h.RegressedRounds)
		if h.Phase == "verifying" && cutover == "" {
			if final.ReleaseGraph == nil || final.ReleaseGraph.TargetReleaseSetID == "" {
				t.Fatal("cutover lost graph identity")
			}
			cutover = final.ReleaseGraph.TargetReleaseSetID
			proof["health_started_at"] = h.StartedAt
			p.assertGraph(t, cutover)
			p.assertConfig(t, config)
			p.graphReport(t, frontend, backend.DeploymentID, cutover, "tested", "https")
			if name == "rollback" {
				p.regressGraphBackend(t, backend, frontend)
				regressed = true
			}
			mustProjectWorkerNative(t, p.h.RestartAPID())
			restarted := p.status(t, id)
			if restarted.Health.Phase != "verifying" || !restarted.Health.Deadline.Equal(h.Deadline) {
				t.Fatal("post-cutover restart reset health")
			}
		}
		if cutover != "" && h.CheckedAt != nil && !h.CheckedAt.Before(h.StartedAt) && h.Verdict == "healthy" && (len(windows) == 0 || h.CheckedAt.After(windows[len(windows)-1])) {
			windows = append(windows, *h.CheckedAt)
		}
		if h.Phase == "succeeded" {
			if name != "success" {
				t.Fatal("API regression was promoted")
			}
			return true
		}
		if h.Phase == "failed" {
			if name == "success" {
				t.Fatal("healthy graph failed", final.Error)
			}
			return true
		}
		return false
	})
	if name != "preflight_refusal" && preflightRounds < api.ProjectReleaseVerificationRounds {
		t.Fatal("cutover lacked two real preflight rounds")
	}
	if regressed && regressions < api.ProjectReleaseVerificationRounds {
		t.Fatal("API failure lacked two real regression rounds")
	}
	selected := baseline
	if name == "success" {
		if len(windows) < 2 || windows[1].Sub(windows[0]) < time.Minute {
			t.Fatal("missing fresh post-cutover healthy windows")
		}
		selected = cutover
		backend = p.graphInstance(t, p.apps["backend"].ID, backend.DeploymentID)
		mustProjectWorkerNative(t, p.owner.Park(t.Context(), p.apps["backend"].Slug))
		_, err = e2etest.WaitForAppParked(t.Context(), t, p.h.Pool, p.apps["backend"].ID, time.Minute)
		mustProjectWorkerNative(t, err)
		var wake projectWorkerNativeReport
		waitProjectWorkerNative(t, 2*time.Minute, "selected API wake through frontend binding", func() bool {
			body, status := graphGuestRequest(t, frontend, "/", http.MethodGet)
			if status != http.StatusOK {
				return false
			}
			wake = decodeGraphReport(t, body)
			return wake.BackendInstanceID != "" && wake.BackendInstanceID != backend.ID
		})
		wake = p.graphReport(t, frontend, backend.DeploymentID, cutover, "tested", "https")
		proof["parked_backend_instance"], proof["wake_report"], proof["park_wake"] = backend.ID, wake, true
		p.assertConfig(t, config)
	} else {
		if name == "preflight_refusal" {
			if cutover != "" {
				t.Fatal("preflight failure published a graph")
			}
		} else {
			if final.RollbackStatus != "rolled_back" || final.ReleaseGraph == nil {
				t.Fatal("both members were not restored")
			}
			selected = final.ReleaseGraph.RestoredTargetReleaseSetID
		}
		p.assertConfig(t, p.targetConfig.ConfigHash)
		p.assertHTTP(t, "baseline", "baseline-config")
	}
	p.assertGraph(t, selected)
	p.assertArtifacts(t, final)
	publications := p.publicationCount(t) - before
	want := map[string]int{"preflight_refusal": 0, "rollback": 2, "success": 1}[name]
	if publications != want {
		t.Fatal("service graph publication duplicated or lost", publications)
	}
	proof["phase"], proof["cutover_graph"], proof["selected_graph"], proof["publications"] = final.Health.Phase, cutover, selected, publications
	proof["preflight_rounds"], proof["regressed_rounds"], proof["healthy_windows"] = preflightRounds, regressions, windows
	proof["artifact_reused"], proof["graph_config_consistent"], proof["restart_verifying"] = true, true, cutover != ""
	return proof
}

func (p *environmentPromotionNative) regressGraphBackend(t *testing.T, backend, frontend state.Instance) {
	t.Helper()
	_, status := graphGuestRequest(t, backend, "/control/regress", http.MethodPost)
	if status != http.StatusNoContent {
		t.Fatal("API rejected guest regression")
	}
	_, status = graphGuestRequest(t, frontend, "/critical", http.MethodGet)
	if status != http.StatusServiceUnavailable {
		t.Fatal("frontend critical probe ignored API failure")
	}
}

func (p *environmentPromotionNative) qualifyGraphIsolation(t *testing.T, frontend, backend state.Instance, id string, foreign *projectWorkerNativeFixture) map[string]any {
	t.Helper()
	spoof := map[string]string{api.ReleaseHeader: id, api.TargetDeploymentHeader: backend.DeploymentID, api.RevisionHeader: backend.DeploymentID}
	statuses := map[string]int{}
	statuses["same_tenant"] = graphAttack(t, p.h.EdgeURL(), p.apps["frontend"].Slug+".apps.test.example", "", spoof)
	statuses["foreign_tenant"] = graphAttack(t, p.h.EdgeURL(), foreign.apps["frontend"].Slug+".apps.test.example", "https://"+p.apps["backend"].Slug+".internal", spoof)
	statuses["candidate_wrong_hint"] = graphAttack(t, "http://"+net.JoinHostPort(frontend.HostIP, "8080"), "", "", map[string]string{api.ReleaseHeader: p.baseline.ID})
	for _, status := range statuses {
		if status != http.StatusForbidden && status != http.StatusNotFound && status != http.StatusConflict {
			t.Fatal("forged graph claim was not explicitly denied", status)
		}
	}
	edge := p.qualifyGraphEdgeIsolation(t, frontend.DeploymentID, id)
	if foreign.accountID == p.accountID {
		t.Fatal("foreign isolation fixture shares owner")
	}
	return map[string]any{"statuses": statuses, "edge_statuses": edge, "owner_account": p.accountID, "foreign_account": foreign.accountID}
}

func (p *environmentPromotionNative) qualifyGraphEdgeIsolation(t *testing.T, deployment, private string) map[string]int {
	t.Helper()
	statuses := map[string]int{}
	for header, value := range map[string]string{api.ReleaseHeader: private, api.TargetDeploymentHeader: deployment, api.RevisionHeader: deployment} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.h.EdgeURL()+"/", nil)
		mustProjectWorkerNative(t, err)
		request.Host = p.apps["frontend"].Slug + ".apps.test.example"
		request.Header.Set(header, value)
		response, err := (&http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
		mustProjectWorkerNative(t, err)
		body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		mustProjectWorkerNative(t, response.Body.Close())
		mustProjectWorkerNative(t, err)
		statuses[header] = response.StatusCode
		if response.StatusCode == http.StatusOK {
			report := decodeGraphReport(t, body)
			if report.Generation != "baseline" || report.BackendGeneration != "baseline" || report.ReleaseID != p.baseline.ID || report.DeploymentID == deployment {
				t.Fatal("public forged claim entered the candidate graph")
			}
		} else if response.StatusCode != 400 && response.StatusCode != 403 && response.StatusCode != 404 && response.StatusCode != 409 && response.StatusCode != 410 {
			t.Fatal("public forged claim lacked a routing denial", response.StatusCode)
		}
	}
	return statuses
}
