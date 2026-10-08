package main

// adr: 733
// Crash snapshot API: operator and plan gates, settings, manual capture
// rules, forking a capture, and IDOR.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func crashEnv(t *testing.T, plan api.Plan) (testEnv, state.App, state.Deployment) {
	t.Helper()
	e := setup(t, plan)
	e.s.WithAppForksEnabled(true)
	e.s.WithCrashSnapshotsEnabled(true)
	app, dep := seedAppTaskDeployment(t, e, "my-api")
	return e, app, dep
}

func TestCrashSnapshots_DisabledByDefault(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedAppTaskDeployment(t, e, "my-api")
	rec := e.do(t, http.MethodGet, "/v1/apps/my-api/crash-snapshots", nil, nil)
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), api.CodeCrashSnapshotsNotEnabled) {
		t.Fatalf("disabled = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCrashSnapshots_PlanGate(t *testing.T) {
	e, _, _ := crashEnv(t, api.PlanHobby)
	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/crash-snapshots", nil, nil)
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanAppForksNotAllowed)
}

func TestCrashSnapshots_SettingsRoundTrip(t *testing.T) {
	e, _, _ := crashEnv(t, api.PlanPro)
	rec := e.do(t, http.MethodPut, "/v1/apps/my-api/crash-snapshots/settings", api.CrashSnapshotSettingsRequest{Enabled: true}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("put settings = %d %s", rec.Code, rec.Body.String())
	}
	var got api.CrashSnapshotSettingsResponse
	_ = json.Unmarshal(e.do(t, http.MethodGet, "/v1/apps/my-api/crash-snapshots/settings", nil, nil).Body.Bytes(), &got)
	if !got.Enabled || got.UpdatedAt == nil {
		t.Fatalf("settings = %+v, want enabled", got)
	}
}

func TestCrashSnapshots_ManualCaptureAndFork(t *testing.T) {
	e, app, dep := crashEnv(t, api.PlanPro)
	ctx := context.Background()

	if rec := e.do(t, http.MethodPost, "/v1/apps/my-api/crash-snapshots", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("capture with no running instance = %d, want 409", rec.Code)
	}
	if _, err := e.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), 256, "node-1", "wake-1", string(state.InstanceModeNormal)); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/crash-snapshots", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("manual capture = %d %s", rec.Code, rec.Body.String())
	}
	var capture api.CrashCaptureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &capture); err != nil || capture.Trigger != "manual" || capture.Status != "requested" {
		t.Fatalf("capture = %+v, %v", capture, err)
	}
	if rec := e.do(t, http.MethodPost, "/v1/apps/my-api/crash-snapshots", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("second capture while in flight = %d, want 409", rec.Code)
	}
	if rec := e.do(t, http.MethodPost, "/v1/apps/my-api/crash-snapshots/"+capture.ID+"/fork", api.CreateAppForkRequest{}, nil); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), api.CodeCrashCaptureNotReady) {
		t.Fatalf("fork of a requested capture = %d %s, want 409 not ready", rec.Code, rec.Body.String())
	}

	// schedd's part, done directly against the store.
	if _, err := e.store.ClaimNextCrashCapture(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := e.store.CompleteCrashCapture(ctx, state.CompleteCrashCaptureParams{
		ID: capture.ID, StorageKey: "snap/d/warm/captures/x/v2/mem", VMStateStorageKey: "snap/d/warm/captures/x/v2/vmstate",
		FCVersion: "1.7.0", MemBytes: 1, CapturedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	rec = e.do(t, http.MethodPost, "/v1/apps/my-api/crash-snapshots/"+capture.ID+"/fork", api.CreateAppForkRequest{}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("fork of a ready capture = %d %s", rec.Code, rec.Body.String())
	}
	fork := decodeAppFork(t, rec.Body.Bytes())
	stored, err := e.store.AppForkForApp(ctx, app.ID, fork.ID)
	if err != nil || stored.CrashCaptureID == nil || *stored.CrashCaptureID != capture.ID || fork.AccessToken == "" {
		t.Fatalf("fork = %+v stored=%+v, %v; want pinned to the capture with a token", fork, stored, err)
	}

	var list api.CrashCaptureListResponse
	_ = json.Unmarshal(e.do(t, http.MethodGet, "/v1/apps/my-api/crash-snapshots", nil, nil).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Status != "ready" || list.Items[0].ExpiresAt == nil {
		t.Fatalf("list = %+v", list)
	}
}

func TestCrashSnapshots_CrossAccount404(t *testing.T) {
	e, _, _ := crashEnv(t, api.PlanPro)
	other := state.NewMemStore()
	otherAcct, _ := other.CreateAccount(t.Context(), "other@pro.com", api.PlanPro)
	mustSeedAppFor(t, other, otherAcct.ID, "their-api")
	if rec := e.do(t, http.MethodGet, "/v1/apps/their-api/crash-snapshots", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account list = %d, want 404", rec.Code)
	}
}
