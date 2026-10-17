//go:build metal

package e2e_test

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectRollbackParkedColdBootMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "retention_cold", "")
}
func TestProjectRollbackExpiredBaselineMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "retention_expired", "")
}

func (f *projectWorkerNativeFixture) qualifyRollbackRetention(t *testing.T, attempt, scenario string, accepted api.ApplyResponse, checkpoint state.ProjectCanaryPolicyState) {
	t.Helper()
	if scenario == "retention_expired" {
		f.qualifyExpiredRollback(t, attempt, accepted, checkpoint)
		return
	}
	app := f.apps["frontend"]
	baselineDeployment := f.baseline.Members[0].DeploymentID
	mustProjectWorkerNative(t, f.h.KillAPID())
	f.apidStopped = true
	waitProjectWorkerNative(t, 2*time.Minute, "baseline parked with zero residency", func() bool {
		instances, err := f.store.ListInstancesForApp(t.Context(), app.ID)
		mustProjectWorkerNative(t, err)
		parked := false
		for _, i := range instances {
			if i.DeploymentID != baselineDeployment {
				continue
			}
			if i.State == string(state.StateParked) {
				parked = true
			}
			if i.State == string(state.StateRunning) || i.State == string(state.StateWaking) || i.State == string(state.StateColdBooting) {
				return false
			}
		}
		return parked
	})
	mustProjectWorkerNative(t, f.h.RestartAPID())
	f.apidStopped = false
	invalidatedAt := time.Now().UTC()
	restart, err := f.owner.RestartAppFresh(t.Context(), app.Slug)
	mustProjectWorkerNative(t, err)
	waitProjectWorkerNative(t, 2*time.Minute, "fresh restart completed", func() bool {
		status, err := f.owner.GetRuntimeConfigRestartStatus(t.Context(), app.Slug, restart.WakeID)
		mustProjectWorkerNative(t, err)
		if status.Status == "failed" {
			t.Fatalf("fresh restart failed: %s", status.FailureReason)
		}
		return status.Status == "succeeded"
	})
	f.regressNativeDeployment(t, accepted.Builds[0].DeploymentID)
	mustProjectWorkerNative(t, f.h.RestartAPID())
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	final := f.receipt(t, attempt, "rolled_back")
	if final.Apply.SafeRelease.RestoredReleaseID != f.baseline.ID {
		t.Fatal("rollback restored a different graph")
	}
	f.assertGraph(t, f.baseline.ID)
	f.assertHTTP(t, "stable", "stable-config")
	var cold bool
	mustProjectWorkerNative(t, f.h.Pool.QueryRow(t.Context(), `SELECT EXISTS (
   SELECT 1 FROM events e JOIN instances i ON i.id::text=e.data->>'instance_id'
   WHERE i.deployment_id=$1 AND e.actor='schedd' AND e.kind='wake.boot_started'
     AND e.data->>'method'='cold_boot' AND e.at >= $2)`, baselineDeployment, invalidatedAt).Scan(&cold))
	if !cold {
		t.Fatal("retained baseline did not cold boot after snapshot invalidation")
	}
	f.assertNoResubmission(t, accepted)
	t.Log("rollback retention native: scenario=cold_boot parked=true cold_boot=true restart=true graph_config=restored status=rolled_back")
}

func (f *projectWorkerNativeFixture) regressNativeDeployment(t *testing.T, deployment string) {
	t.Helper()
	waitProjectWorkerNative(t, time.Minute, "running candidate regression", func() bool {
		instances, err := f.store.ListInstancesForApp(t.Context(), f.apps["frontend"].ID)
		mustProjectWorkerNative(t, err)
		for _, i := range instances {
			if i.DeploymentID != deployment || i.State != string(state.StateRunning) {
				continue
			}
			_, status := projectWorkerNativeRequest(t, "http://"+net.JoinHostPort(i.HostIP, "8080")+"/control/regress", "", http.MethodPost)
			if status != http.StatusNoContent {
				t.Fatalf("regression failed: %d", status)
			}
			return true
		}
		return false
	})
}

func (f *projectWorkerNativeFixture) qualifyExpiredRollback(t *testing.T, attempt string, accepted api.ApplyResponse, checkpoint state.ProjectCanaryPolicyState) {
	t.Helper()
	f.disableNativeProbes(t)
	request := api.ProjectReleaseControlRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedGeneration: checkpoint.Generation}
	f.controlWithLostResponse(t, attempt, "abort", request)
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+api.ProjectReleaseVerificationInterval, "bounded rollback blocker", func() bool {
		receipt, err := f.owner.GetProjectSubmission(t.Context(), attempt)
		mustProjectWorkerNative(t, err)
		return receipt.Apply.SafeRelease.Status == "rollback_failed"
	})
	var expiresAt time.Time
	mustProjectWorkerNative(t, f.h.Pool.QueryRow(t.Context(), "SELECT expires_at FROM project_release_sets WHERE id=$1", f.baseline.ID).Scan(&expiresAt))
	config, err := f.owner.GetProjectEnvironmentConfig(t.Context(), f.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	for time.Now().Before(expiresAt) {
		f.assertGraph(t, checkpoint.CandidateReleaseID)
		f.assertNewWorkBlocked(t)
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(time.Second):
		}
	}
	mustProjectWorkerNative(t, f.h.SetAPIDEnv("FAAS_ROUTE_PROBE_URL", f.h.GatewayURL))
	mustProjectWorkerNative(t, f.h.RestartAPID())
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	current, err := f.store.ProjectCanaryCheckpoint(t.Context(), f.accountID, attempt)
	mustProjectWorkerNative(t, err)
	request = api.ProjectReleaseControlRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedGeneration: current.Generation}
	_, err = f.owner.ControlProjectRelease(t.Context(), attempt, "recover", request)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusGone || problem.Problem.Code != "project_release_baseline_expired" {
		t.Fatalf("expired recovery lacked explicit blocker: %v", err)
	}
	after, err := f.store.ProjectCanaryCheckpoint(t.Context(), f.accountID, attempt)
	mustProjectWorkerNative(t, err)
	if after.Generation != current.Generation || after.Control.RequestID != current.Control.RequestID {
		t.Fatal("expired request changed durable decision")
	}
	f.assertGraph(t, checkpoint.CandidateReleaseID)
	latest, err := f.owner.GetProjectEnvironmentConfig(t.Context(), f.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	if latest.Version != config.Version || latest.ConfigHash != config.ConfigHash {
		t.Fatal("expired rollback changed configuration")
	}
	f.assertNewWorkBlocked(t)
	f.assertNoResubmission(t, accepted)
	t.Log("rollback retention native: scenario=expired clock=real recovery=denied restart=true graph_config=preserved status=rollback_failed")
}
