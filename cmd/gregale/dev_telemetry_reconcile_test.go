package main_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// H10-34: a dropped build-log stream never delivered the image_build
// completion, so the human receipt said build=0ms for a 4-minute build.
func TestDevReceiptReconcilesPersistedStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments/dep-1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dep-1","status":"live","stage_state":{"current":"live","history":[{"name":"image_build","duration_ms":255500,"status":"completed"},{"name":"readiness","duration_ms":1700,"status":"completed"}]}}`))
	}))
	t.Cleanup(srv.Close)

	tracker := main.NewDevPhaseTracker()
	tracker.SetDeploymentID("dep-1")
	tracker.ObserveStage("image_build", main.StageStatusInProgress, 0, "")
	tracker.ReconcileFinalStages(t.Context(), api.NewClient(srv.URL, "fp_test_x"))

	got := map[string]main.DevPhaseTiming{}
	for _, phase := range tracker.Receipt("live").Phases {
		got[phase.Phase] = phase
	}
	if b := got[main.DevPhaseBuild]; b.Status != "completed" || b.DurationMS != 255500 {
		t.Fatalf("build phase = %+v, want the persisted 255.5 s completion", b)
	}
	if r := got[main.DevPhaseReady]; r.DurationMS != 1700 {
		t.Fatalf("ready phase = %+v, want 1.7 s", r)
	}
}
