package mcpmanagement

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func watchHealthy(now time.Time) api.AppHealthResponse {
	return api.AppHealthResponse{Status: "healthy", LatestDeploymentID: "new", ServingDeploymentIDs: []string{"new"}, EvaluatedAt: now.Add(-time.Second).Format(time.RFC3339Nano), ValidForSeconds: 120}
}
func TestWatchRolloutCompletionEvidence(t *testing.T) {
	now := time.Now()
	for _, scenario := range []string{"request", "staged", "service", "handoff_missing", "gateway_pending", "aborted", "stale", "neighbor", "worker", "worker_draining", "build_running"} {
		t.Run(scenario, func(t *testing.T) {
			d := api.DeploymentResponse{ID: "new", Status: "live", TrafficPercent: 100, RolloutState: "complete"}
			out := watchRolloutResult{Mode: api.ExecutionModeRequest, CheckedAt: now, Live: true, Health: watchHealthy(now)}
			want := scenario == "request" || scenario == "service" || scenario == "worker"
			switch scenario {
			case "staged":
				d.TrafficPercent = 0
			case "service", "handoff_missing", "gateway_pending", "aborted":
				out.Mode = api.ExecutionModeService
				if scenario != "handoff_missing" {
					d.ServiceRolloutHandoff = &api.ServiceRolloutHandoffResponse{Action: "promote", CompletedAt: &now}
				}
				if scenario == "gateway_pending" {
					d.ServiceRolloutHandoff.MissingGateways = []string{"gateway"}
				}
				if scenario == "aborted" {
					d.ServiceRolloutHandoff.Action = "abort"
				}
			case "stale":
				out.Health.EvaluatedAt = now.Add(-time.Hour).Format(time.RFC3339Nano)
			case "neighbor":
				out.Health.ServingDeploymentIDs = []string{"old"}
			case "worker", "worker_draining":
				out.Mode = api.ExecutionModeWorker
				out.Health.Status = "unknown"
				out.Worker = &api.WorkerRolloutObservation{SelectedDeploymentID: "new", Running: 1, GenerationConverged: scenario == "worker"}
			case "build_running":
				out.Build = &api.BuildResponse{Status: "running"}
			}
			deriveRolloutWatch(&out, d)
			if out.Completed != want {
				t.Fatalf("completion=%v want=%v blockers=%v", out.Completed, want, out.Blockers)
			}
			if scenario == "worker" && out.Healthy {
				t.Fatal("worker residency implied health")
			}
		})
	}
}

func TestWatchRolloutBoundedReadAndCrossScope(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, false)
	start := time.Now()
	out := callTool(t, ctx, cs, "gregale_watch_rollout", map[string]any{"app": "alpha", "deployment_id": "release-base", "wait_seconds": 1}, false)
	if string(out["watch_timed_out"]) != "true" || string(out["completed"]) != "false" || time.Since(start) > 3*time.Second {
		t.Fatal("watch did not return bounded pending snapshot")
	}
	if f.writes != 0 {
		t.Fatal("watch changed customer intent")
	}
	f.mu.Lock()
	d := f.deployments["release-base"]
	d.Scope = "neighbor"
	f.deployments[d.ID] = d
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_watch_rollout", map[string]any{"app": "alpha", "deployment_id": "release-base", "wait_seconds": 1}, true)
	callTool(t, ctx, cs, "gregale_watch_rollout", map[string]any{"app": "alpha", "deployment_id": "release-base", "wait_seconds": 46}, true)
}

func TestWatchWorkerCompletesResidencyOnly(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	f.app.Manifest.ExecutionMode = api.ExecutionModeWorker
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), false))
	callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
	out := callTool(t, ctx, cs, "gregale_watch_rollout", map[string]any{"app": "alpha", "deployment_id": "release-new", "wait_seconds": 1}, false)
	if string(out["completed"]) != "true" || string(out["healthy"]) != "false" {
		t.Fatal("residency was confused with workload health")
	}
	var worker api.WorkerRolloutObservation
	if json.Unmarshal(out["worker_observation"], &worker) != nil || worker.QueueProcessingConfirmed {
		t.Fatal("watch claimed queue completion")
	}
}
