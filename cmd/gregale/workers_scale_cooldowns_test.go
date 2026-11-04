package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: `gregale workers scale h14-wk --min 0 --max 1` failed
// with "scale_out_cooldown_s must be in [1, 3600]; got 0": the worker was
// deployed without a scaling policy, so the command sent a zero-value one
// (Hunt 14 H14-20).
func TestWorkersScaleSendsValidCooldowns(t *testing.T) {
	var got api.UpdateAppRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		app := api.AppResponse{ID: "app-w", Slug: "h14-wk", Manifest: api.AppManifest{
			ExecutionMode:  api.ExecutionModeWorker,
			WorkerReplicas: &api.WorkerScaling{Min: 1, Max: 1, Metric: "queue_lag", Target: 100},
		}}
		if r.Method == http.MethodPatch {
			_ = json.NewDecoder(r.Body).Decode(&got)
		}
		_ = json.NewEncoder(w).Encode(app)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOutput()
	t.Cleanup(main.ResetJSONOutput)
	_, restore := main.CaptureStdout(t)
	code := main.CmdWorkersScale([]string{"h14-wk", "--min", "0", "--max", "1"})
	restore()
	if code != 0 || got.ScalingPolicy == nil {
		t.Fatalf("exit %d, scaling policy %+v", code, got.ScalingPolicy)
	}
	if sp := got.ScalingPolicy; sp.ScaleOutCooldownS < api.MinScaleOutCooldownS || sp.ScaleInCooldownS < api.MinScaleInCooldownS {
		t.Fatalf("cooldowns out %d in %d, want at least %d and %d", sp.ScaleOutCooldownS, sp.ScaleInCooldownS, api.MinScaleOutCooldownS, api.MinScaleInCooldownS)
	}
}
