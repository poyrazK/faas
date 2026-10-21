//go:build promotionacceptance && metal

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentPromotionSuccessMetal(t *testing.T) {
	qualifyEnvironmentPromotionNative(t, false)
}

func TestProjectEnvironmentPromotionRollbackMetal(t *testing.T) {
	qualifyEnvironmentPromotionNative(t, true)
}

// Real clocks and daemon-owned evidence only. In particular, these scenarios
// never update checkpoint times, insert startup/job proofs or publish a
// promotion's candidate/rollback graph directly through the store.
func qualifyEnvironmentPromotionNative(t *testing.T, rollback bool) {
	p := newEnvironmentPromotionNative(t)
	p.baselineSamples(t)
	old := p.baselineWorker(t)
	p.controlWorker(t, old, "crash-next", http.StatusNoContent)
	retryID := p.enqueue(t, "pinned-retry", false)
	waitProjectWorkerNative(t, time.Minute, "real failed delivery and deferred retry", func() bool {
		inv, err := p.store.InvocationByID(t.Context(), retryID)
		mustProjectWorkerNative(t, err)
		return inv.State == state.InvocationPending && inv.Attempts == 1
	})
	beforeRelease, beforeDeployment := p.pin(t, retryID)
	if beforeRelease != p.baseline.ID || beforeDeployment != old.DeploymentID {
		t.Fatal("first job admission did not pin the production baseline")
	}
	preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "production", "staging", true, true, false, true)
	mustProjectWorkerNative(t, err)
	if !preview.CanPromote || preview.Health == nil || preview.Health.Worker == nil || preview.PromotionToken == "" {
		t.Fatalf("native worker promotion refused: %v", preview.BlockingReasons)
	}
	request := api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken}
	key := uuid.NewString()
	accepted, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	mustProjectWorkerNative(t, err)
	if accepted.Status != "running" || accepted.PromotionID == "" {
		t.Fatal("promotion was not admitted as a durable operation")
	}
	var preflight api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, time.Minute, "held native promotion candidate", func() bool {
		preflight = p.status(t, accepted.PromotionID)
		return preflight.Health.Phase == "preflight" && preflight.Health.Worker != nil && preflight.Health.Worker.InstanceID != ""
	})
	mustProjectWorkerNative(t, p.h.KillAPID())
	p.apidStopped = true
	candidate := p.worker(t, preflight.Health.Worker.DeploymentID)
	assertProjectWorkerIsolated(t, candidate, "tested", "tested-config")
	if !projectWorkerNativeState(t, candidate).DestinationSecretMatches {
		t.Fatal("candidate did not receive destination credentials")
	}
	p.assertGraph(t, p.baseline.ID)
	p.assertConfigOffline(t, p.targetConfig.ConfigHash)
	p.killWorkerVM(t, candidate)
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	candidate = p.replacement(t, candidate)
	assertProjectWorkerIsolated(t, candidate, "tested", "tested-config")
	if !projectWorkerNativeState(t, candidate).DestinationSecretMatches {
		t.Fatal("recovered candidate lost destination credentials")
	}
	// The release owner is absent; normal production work must still reach
	// the retained baseline, while the recovered candidate receives none.
	p.job(t, "owner-down", "baseline", "baseline-config")
	p.assertGraph(t, p.baseline.ID)
	checkpoint, err := p.store.ProjectEnvironmentPromotionHealth(t.Context(), p.accountID, accepted.PromotionID)
	mustProjectWorkerNative(t, err)
	if checkpoint.Phase != "preflight" || !checkpoint.Deadline.Equal(preflight.Health.Deadline) || checkpoint.Generation < preflight.Health.Generation {
		t.Fatal("owner loss changed the durable preflight deadline or phase")
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	p.apidStopped = false
	replayed, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	mustProjectWorkerNative(t, err)
	if replayed.PromotionID != accepted.PromotionID {
		t.Fatal("idempotent admission created another promotion")
	}
	var cutover api.ProjectEnvironmentPromotionStatusResponse
	preflightRounds := 0
	waitProjectWorkerNative(t, 5*time.Minute, "two real preflight rounds and graph cutover", func() bool {
		s := p.status(t, accepted.PromotionID)
		if s.Health.Phase == "preflight" || s.Health.Phase == "cutover" {
			preflightRounds = max(preflightRounds, s.Health.HealthyRounds)
			if !s.Health.Deadline.Equal(checkpoint.Deadline) {
				t.Fatal("restart reset the preflight deadline")
			}
			p.assertGraph(t, p.baseline.ID)
			assertProjectWorkerIsolated(t, candidate, "tested", "tested-config")
			return false
		}
		if s.Health.Phase != "verifying" || s.ReleaseGraph == nil || s.ReleaseGraph.TargetReleaseSetID == "" {
			t.Fatalf("promotion ended before cutover: phase=%s status=%s", s.Health.Phase, s.Status)
		}
		cutover = s
		return true
	})
	if preflightRounds < api.ProjectReleaseVerificationRounds {
		t.Fatal("native gate did not observe both preflight rounds")
	}
	p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
	p.assertConfig(t, p.sourceConfig.ConfigHash)
	p.assertHTTP(t, "tested", "tested-config")
	p.assertArtifacts(t, cutover)
	// This deferred delivery must still be pending at the observed cutover.
	// A slow run that already retried cannot claim cross-cutover pin coverage.
	retry, err := p.store.InvocationByID(t.Context(), retryID)
	mustProjectWorkerNative(t, err)
	if retry.State != state.InvocationPending || retry.Attempts != 1 {
		t.Fatal("retry happened before cutover; cross-cutover scenario was not exercised")
	}
	baselineWorker := p.worker(t, beforeDeployment)
	p.killWorkerVM(t, baselineWorker)
	p.replacement(t, baselineWorker)
	// Recover the post-cutover owner too. Phase and original deadline survive;
	// candidate/retained pool selection must not depend on process memory.
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	mustProjectWorkerNative(t, p.h.RestartAPID())
	recovered := p.status(t, accepted.PromotionID)
	if recovered.Health.Phase != "verifying" || !recovered.Health.Deadline.Equal(cutover.Health.Deadline) || recovered.Health.Generation < cutover.Health.Generation {
		t.Fatal("post-cutover restart lost the health checkpoint")
	}
	if rollback {
		_, status := projectWorkerNativeRequest(t, p.h.EdgeURL()+"/control/regress", p.apps["frontend"].Slug+".apps.test.example", http.MethodPost)
		if status != http.StatusNoContent {
			t.Fatal("candidate did not accept the critical-probe fault")
		}
	}
	var final api.ProjectEnvironmentPromotionStatusResponse
	var windows []time.Time
	regressedRounds := 0
	jobs := 0
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "native promotion terminal health", func() bool {
		final = p.status(t, accepted.PromotionID)
		h := final.Health
		regressedRounds = max(regressedRounds, h.RegressedRounds)
		if h.CheckedAt != nil && h.WorkerHealth != nil && h.WorkerHealth.Status == "healthy" &&
			(len(windows) == 0 || h.CheckedAt.After(windows[len(windows)-1])) {
			windows = append(windows, *h.CheckedAt)
		}
		if h.Phase == "succeeded" && !rollback || h.Phase == "failed" && rollback && final.RollbackStatus == "rolled_back" {
			return true
		}
		if h.Phase != "verifying" && h.Phase != "finalizing" && h.Phase != "rolling_back" {
			t.Fatalf("unexpected terminal promotion health: phase=%s rollback=%s", h.Phase, final.RollbackStatus)
		}
		if h.Phase == "verifying" && (!rollback || jobs < api.ProjectWorkerHealthMinAttempts) {
			inv := p.job(t, fmt.Sprintf("candidate-%d", jobs), "tested", "tested-config")
			release, deployment := p.pin(t, inv.ID)
			if release != cutover.ReleaseGraph.TargetReleaseSetID || deployment != candidate.DeploymentID {
				t.Fatal("new job admission did not switch with the project graph")
			}
			jobs++
		}
		time.Sleep(time.Second)
		return false
	})
	if jobs < api.ProjectWorkerHealthMinAttempts || !rollback && (len(windows) < 2 || final.Health.HealthyRounds < api.ProjectReleaseVerificationRounds) || rollback && regressedRounds < api.ProjectReleaseVerificationRounds {
		t.Fatal("native promotion lacks fresh post-cutover job/probe rounds")
	}
	status := "succeeded"
	if rollback {
		status = "rolled_back"
		if final.ReleaseGraph == nil || final.ReleaseGraph.RestoredTargetReleaseSetID == "" {
			t.Fatal("rollback omitted restored graph identity")
		}
		p.assertGraph(t, final.ReleaseGraph.RestoredTargetReleaseSetID)
		p.assertConfig(t, p.targetConfig.ConfigHash)
		p.assertHTTP(t, "baseline", "baseline-config")
		p.job(t, "restored-admission", "baseline", "baseline-config")
	} else {
		p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
		p.assertConfig(t, p.sourceConfig.ConfigHash)
		p.assertHTTP(t, "tested", "tested-config")
	}
	waitProjectWorkerNative(t, 9*time.Minute, "pinned retry on recovered retained pool", func() bool {
		retry, err = p.store.InvocationByID(t.Context(), retryID)
		mustProjectWorkerNative(t, err)
		if retry.State == state.InvocationDeadLetter || retry.State == state.InvocationFailed {
			t.Fatal("pinned retry failed on retained worker")
		}
		return retry.State == state.InvocationCompleted
	})
	var result projectWorkerNativeReport
	mustProjectWorkerNative(t, json.Unmarshal(retry.Result, &result))
	afterRelease, afterDeployment := p.pin(t, retryID)
	if retry.Attempts != 2 || afterRelease != beforeRelease || afterDeployment != beforeDeployment || retry.CompletedAt == nil ||
		!retry.CompletedAt.After(cutover.Health.StartedAt) || result.Generation != "baseline" || result.Mode != "baseline-config" || !result.DestinationSecretMatches {
		t.Fatal("retry did not preserve its first-admission artifact, graph and configuration after cutover")
	}
	for _, id := range p.completed {
		inv, err := p.store.InvocationByID(t.Context(), id)
		mustProjectWorkerNative(t, err)
		if inv.State != state.InvocationCompleted || inv.Attempts != 1 {
			t.Fatal("promotion or rollback replayed a completed job")
		}
	}
	p.assertArtifacts(t, final)
	p.cleanup(t)
	if t.Failed() {
		return
	}
	evidence := map[string]any{"schema_version": 1, "scenario": status, "promotion_id": accepted.PromotionID,
		"preflight_rounds": preflightRounds, "healthy_windows": windows, "regressed_rounds": regressedRounds,
		"candidate_jobs": jobs, "retry_attempts": retry.Attempts, "retry_release_id": afterRelease, "retry_deployment_id": afterDeployment,
		"candidate_release_id": cutover.ReleaseGraph.TargetReleaseSetID, "restart_preflight": true, "restart_verifying": true,
		"candidate_recovered": true, "retained_recovered": true, "candidate_isolated": true, "destination_secrets": true,
		"artifact_reused": true, "idempotent_replay": true, "graph_config_restored": rollback, "cleanup": true}
	raw, err := json.Marshal(evidence)
	mustProjectWorkerNative(t, err)
	t.Logf("environment promotion native: %s", raw)
}

func (p *environmentPromotionNative) pin(t *testing.T, id string) (string, string) {
	t.Helper()
	var release, deployment string
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT release_id::text, deployment_id::text FROM project_worker_release_pins WHERE root_invocation_id=$1", id).Scan(&release, &deployment))
	return release, deployment
}

func (p *environmentPromotionNative) replacement(t *testing.T, previous state.Instance) state.Instance {
	t.Helper()
	var next state.Instance
	waitProjectWorkerNative(t, 2*time.Minute, "daemon-owned worker replacement", func() bool {
		next = p.worker(t, previous.DeploymentID)
		return next.ID != previous.ID
	})
	return next
}

func (p *environmentPromotionNative) assertConfigOffline(t *testing.T, hash string) {
	t.Helper()
	c, err := p.store.ProjectEnvironmentConfigLatest(t.Context(), p.accountID, p.project.ID, "production")
	mustProjectWorkerNative(t, err)
	if c.ConfigHash != hash {
		t.Fatal("preflight changed destination configuration")
	}
}
