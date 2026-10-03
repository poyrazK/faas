package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type failingSafeReleaseLeaseStore struct{ state.Store }

func (failingSafeReleaseLeaseStore) SafeReleaseWorkerLeaseReady(_ context.Context) (bool, error) {
	return false, errors.New("database unavailable")
}

func (failingSafeReleaseLeaseStore) StampSafeReleaseWorkerLease(_ context.Context, _ time.Duration) error {
	return nil
}

func TestCanaryAdmissionFailsClosedOnLeaseReadError(t *testing.T) {
	s := &server{store: failingSafeReleaseLeaseStore{Store: state.NewMemStore()}, log: slog.Default()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/app/deployments", nil)
	if s.admitCanaryDeployment(rec, req, state.Deployment{CanaryTotalSteps: 4}) {
		t.Fatal("canary admitted after lease read error")
	}
	assertProblem(t, rec, http.StatusServiceUnavailable, api.CodeSafeReleaseUnavailable)
}

func TestCanaryAdmissionRequiresFreshReleaseWorkerLease(t *testing.T) {
	e := setup(t, api.PlanPro)
	if rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "canary-lease", RAMMB: 256}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body)
	}
	request := api.CreateDeploymentRequest{
		Image:  "registry.x/example@sha256:" + validDigest(),
		Canary: &api.CanaryPresetSpec{Preset: "balanced"},
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/canary-lease/deployments", request, nil)
	assertProblem(t, rec, http.StatusServiceUnavailable, api.CodeSafeReleaseUnavailable)
	app, err := e.store.AppBySlug(t.Context(), "canary-lease")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LatestDeployment(t.Context(), app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deployment written without worker lease: %v", err)
	}
	if err := e.store.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPost, "/v1/apps/canary-lease/deployments", request, nil)
	if rec.Code != http.StatusAccepted && rec.Code != http.StatusCreated {
		t.Fatalf("canary with worker lease: %d %s", rec.Code, rec.Body)
	}
}
