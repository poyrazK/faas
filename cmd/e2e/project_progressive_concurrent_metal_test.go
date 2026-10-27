//go:build metal

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectProgressiveConcurrentEarlyWinnerMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "concurrent_early", "")
}
func TestProjectProgressiveConcurrentFullWinnerMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "concurrent_full", "")
}
func TestProjectProgressiveConcurrentConfigChangeMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "concurrent_config", "")
}

func (f *projectWorkerNativeFixture) qualifyConcurrentIntent(t *testing.T, attempt, scenario string, accepted api.ApplyResponse, checkpoint state.ProjectCanaryPolicyState) {
	t.Helper()
	stale := api.ProjectReleaseControlRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedGeneration: checkpoint.Generation}
	var winner api.ApplyResponse
	var winnerAttempt string
	graph, err := f.store.ActiveProjectReleaseSet(t.Context(), f.accountID, f.project.ID, "production")
	mustProjectWorkerNative(t, err)
	winnerGraph := graph.ID
	if scenario == "concurrent_config" {
		before, err := f.owner.GetProjectEnvironmentConfig(t.Context(), f.project.Slug, "production")
		mustProjectWorkerNative(t, err)
		// Appending identical bytes is still newer operator intent (ABA protection).
		updated, err := f.owner.UpdateProjectEnvironmentConfig(t.Context(), f.project.Slug, "production", api.UpdateProjectEnvironmentConfigRequest{Values: before.Values})
		mustProjectWorkerNative(t, err)
		if updated.Version <= before.Version {
			t.Fatal("operator configuration did not advance")
		}
	} else {
		raw, err := json.Marshal(api.ProjectReleaseConfigRequest{Env: map[string]map[string]string{"frontend": {"MODE": "winner-config"}}})
		mustProjectWorkerNative(t, err)
		// B uses an atomic verified source release while A still owns a progressive
		// checkpoint. Production intent goes through APID, never synthetic rows.
		ctx := api.ContextWithProjectReleaseVerification(api.ContextWithProjectReleaseConfig(t.Context(), raw))
		source := projectWorkerNativeArchive(t, projectHTTPNativeFiles(f.binary, "winner"))
		plan, err := f.scan(t, ctx, source)
		mustProjectWorkerNative(t, err)
		winnerAttempt = strings.ReplaceAll(uuid.NewString(), "-", "")
		ctx = api.ContextWithDeploymentAttempt(api.ContextWithIdempotencyKey(ctx, winnerAttempt), winnerAttempt)
		winner, err = f.apply(t, ctx, plan.PlanToken, source)
		mustProjectWorkerNative(t, err)
		for _, build := range winner.Builds {
			f.deploymentCounts[build.AppID]++
			ctx, cancel := context.WithTimeout(t.Context(), 16*time.Minute)
			_, _, err := e2etest.WaitForSourceDeployment(ctx, t, f.h.Pool, build.DeploymentID, e2etest.DefaultBuildStallWindow, 15*time.Minute)
			cancel()
			mustProjectWorkerNative(t, err)
		}
		final := f.receipt(t, winnerAttempt, "succeeded")
		winnerGraph = final.Apply.SafeRelease.ReleaseID
		f.assertHTTP(t, "winner", "winner-config")
	}
	config, err := f.owner.GetProjectEnvironmentConfig(t.Context(), f.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	mustProjectWorkerNative(t, f.h.RestartAPID())
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	final := f.receipt(t, attempt, "conflicted")
	if final.Apply.SafeRelease.Message == "" {
		t.Fatal("stale attempt lacks conflict reason")
	}
	for _, action := range []string{"abort", "recover"} {
		_, err := f.owner.ControlProjectRelease(t.Context(), attempt, action, stale)
		var problem *api.APIError
		if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict || problem.Problem.Code != "project_release_control_conflict" {
			t.Fatalf("stale %s did not return ownership conflict: %v", action, err)
		}
	}
	// Wait beyond a real controller observation interval; a late reconciliation
	// must preserve the winning graph/head, not only reject the immediate action.
	deadline := time.Now().Add(api.ProjectReleaseVerificationInterval)
	for time.Now().Before(deadline) {
		f.assertGraph(t, winnerGraph)
		current, err := f.owner.GetProjectEnvironmentConfig(t.Context(), f.project.Slug, "production")
		mustProjectWorkerNative(t, err)
		if current.Version != config.Version || current.ConfigHash != config.ConfigHash || !bytes.Equal(current.Values, config.Values) {
			t.Fatal("stale controller overwrote newer configuration")
		}
		if scenario != "concurrent_config" {
			f.assertHTTP(t, "winner", "winner-config")
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(time.Second):
		}
	}
	f.assertNoResubmission(t, accepted)
	if winnerAttempt != "" {
		f.assertNoResubmission(t, winner)
	}
	t.Logf("concurrent deploy native: scenario=%s percent=%d restart=true stale_controls=denied winner_preserved=true status=conflicted", scenario, checkpoint.Percent)
}
