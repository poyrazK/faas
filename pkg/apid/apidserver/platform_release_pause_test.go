package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022140000 — the break-glass release pause refuses every new
// release, whatever the request carries, and leaves rollbacks alone.

func TestPlatformReleasePauseRefusesReleasesButNotRollbacks(t *testing.T) {
	f := newPostReleaseFixture(t, "pause-app", true, time.Hour)
	e := f.e
	pause := func(on bool) {
		t.Helper()
		if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigPlatformReleasePause, json.RawMessage(strconv.FormatBool(on))); err != nil {
			t.Fatal(err)
		}
	}

	pause(true)
	for name, req := range map[string]api.CreateDeploymentRequest{
		"plain deploy":         {Image: imageRef('a')},
		"freeze override":      {Image: imageRef('b'), OverrideFreeze: "hotfix for incident 4711"},
		"scheduled for later":  {Image: imageRef('c'), ReleaseAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		"non-production scope": {Image: imageRef('d'), Scope: "staging"},
	} {
		rec := guardDo(t, e, http.MethodPost, "/v1/apps/pause-app/deployments", req, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s while paused = %d %s, want 503", name, rec.Code, rec.Body)
		}
		if p := decodeProblem(t, rec.Body.Bytes()); p.Code != api.CodePlatformReleasePaused {
			t.Fatalf("%s problem = %+v", name, p)
		}
	}

	target := f.previous.ID
	rec := guardDo(t, e, http.MethodPost, "/v1/apps/pause-app/rollback", api.RollbackRequest{TargetDeploymentID: &target, Reason: "incident containment"}, nil)
	if rec.Code == http.StatusServiceUnavailable || decodeProblemCode(rec.Body.Bytes()) == api.CodePlatformReleasePaused {
		t.Fatalf("rollback was refused by the release pause: %d %s", rec.Code, rec.Body)
	}

	pause(false)
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/pause-app/deployments", api.CreateDeploymentRequest{Image: imageRef('e')}, nil); rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("deploy after the pause was lifted = %d %s", rec.Code, rec.Body)
	}
}

func decodeProblemCode(body []byte) string {
	var p api.Problem
	_ = json.Unmarshal(body, &p)
	return p.Code
}

// A retry creates a new release. A retried image deployment needs no
// builder, so builderd's pause cannot hold it; apid must refuse it.
func TestPlatformReleasePauseRefusesRetries(t *testing.T) {
	f := newPostReleaseFixture(t, "pause-retry", true, time.Hour)
	e := f.e
	store := *e.Store()
	app, err := store.AppBySlug(t.Context(), "pause-retry")
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, ImageDigest: imageRef('f'), Kind: state.DeploymentKindImage, Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentFailedEx(t.Context(), failed.ID, "boot_failed", "boot failed", "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	pause := func(on bool) {
		t.Helper()
		if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigPlatformReleasePause, json.RawMessage(strconv.FormatBool(on))); err != nil {
			t.Fatal(err)
		}
	}
	retry := api.RetryDeploymentRequest{FromStage: string(state.StageSnapshotPrepare)}

	pause(true)
	before, err := store.ListDeploymentsForApp(t.Context(), app.ID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	rec := guardDo(t, e, http.MethodPost, "/v1/deployments/"+failed.ID+"/retry", retry, nil)
	if rec.Code != http.StatusServiceUnavailable || decodeProblemCode(rec.Body.Bytes()) != api.CodePlatformReleasePaused {
		t.Fatalf("retry while paused = %d %s, want 503 %s", rec.Code, rec.Body, api.CodePlatformReleasePaused)
	}
	if after, err := store.ListDeploymentsForApp(t.Context(), app.ID, 100, 0); err != nil || len(after) != len(before) {
		t.Fatalf("retry while paused created a deployment: %d -> %d, %v", len(before), len(after), err)
	}

	pause(false)
	if rec := guardDo(t, e, http.MethodPost, "/v1/deployments/"+failed.ID+"/retry", retry, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("retry after the pause was lifted = %d %s, want 202", rec.Code, rec.Body)
	}
}

// GitOps environment candidates built from an image or an existing artifact
// need no builder either; while paused the sync prepares nothing.
func TestPlatformReleasePauseHoldsGitOpsCandidates(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	pause := func(on bool) {
		t.Helper()
		if err := (*srv.RuntimeConfig()).Apply(apidserver.RuntimeConfigPlatformReleasePause, json.RawMessage(strconv.FormatBool(on))); err != nil {
			t.Fatal(err)
		}
	}
	intent := &pausePreparationStore{}

	pause(true)
	ready, err := srv.PrepareGitOpsWorkloadCandidatesForTest(t.Context(), intent, state.EnvironmentGitOpsLease{}, environmentsync.Plan{})
	if err != nil || ready || intent.calls != 0 {
		t.Fatalf("paused preparation = %v, %v after %d store calls; want unready with none", ready, err, intent.calls)
	}

	pause(false)
	if _, err := srv.PrepareGitOpsWorkloadCandidatesForTest(t.Context(), intent, state.EnvironmentGitOpsLease{}, environmentsync.Plan{}); err != nil {
		t.Fatal(err)
	}
	if intent.calls == 0 {
		t.Fatal("preparation did not resume after the pause was lifted")
	}
}

// pausePreparationStore counts candidate preparation calls and prepares
// nothing.
type pausePreparationStore struct{ calls int }

func (s *pausePreparationStore) ObserveEnvironmentGitOps(context.Context, state.EnvironmentGitOpsLease, environmentsync.DesiredState) (state.EnvironmentGitOpsObservation, error) {
	return state.EnvironmentGitOpsObservation{}, nil
}
func (s *pausePreparationStore) ApplyEnvironmentGitOps(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]state.EnvironmentGitOpsStep, error) {
	return nil, nil
}
func (s *pausePreparationStore) PreviewEnvironmentGitOpsAdoption(context.Context, string, string) (environmentsync.Plan, error) {
	return environmentsync.Plan{}, nil
}
func (s *pausePreparationStore) AdoptEnvironmentGitOps(context.Context, string, string, string) error {
	return nil
}
func (s *pausePreparationStore) PrepareEnvironmentGitOpsImageCandidates(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]state.EnvironmentWorkloadCandidate, error) {
	s.calls++
	return nil, nil
}
func (s *pausePreparationStore) EnvironmentGitOpsSourceRequests(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]state.EnvironmentWorkloadSourceRequest, error) {
	s.calls++
	return nil, nil
}
func (s *pausePreparationStore) PrepareEnvironmentGitOpsCandidates(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan, map[string]state.EnvironmentWorkloadSourceArtifact) ([]state.EnvironmentWorkloadCandidate, error) {
	s.calls++
	return nil, nil
}
