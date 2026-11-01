package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// PulseWatch F12: with --min 2 set, `gregale app <slug> --min 0` plus a queue
// flag sent min_instances 0 beside a scaling policy copied from the app, which
// still held min_instances 2. The store writes the policy's floor, so the app
// silently kept two warm instances.
func TestAppMinZeroWithQueueFlagsClearsCopiedPolicyFloor(t *testing.T) {
	for name, args := range map[string][]string{
		"app":       {"app", "pulse", "--min", "0", "--max-queue-depth", "40"},
		"app scale": {"app", "pulse", "scale", "--min", "0", "--concurrency-overflow", "queue"},
	} {
		t.Run(name, func(t *testing.T) {
			var patched *api.UpdateAppRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/pulse" {
					http.Error(w, `{"title":"not found","status":404}`, http.StatusNotFound)
					return
				}
				if r.Method == http.MethodPatch {
					body, _ := io.ReadAll(r.Body)
					patched = &api.UpdateAppRequest{}
					if err := json.Unmarshal(body, patched); err != nil {
						t.Errorf("decode PATCH body: %v", err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "pulse", MinInstances: 2,
					ScalingPolicy: &api.ScalingPolicy{MinInstances: 2, MaxInstances: 4, ScaleOutCooldownS: 5, ScaleInCooldownS: 60}})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			_, restoreOut := main.CaptureStdout(t)
			stderr, restoreErr := main.CaptureStderr(t)
			code := main.Run(args)
			restoreErr()
			restoreOut()
			if code != 0 {
				t.Fatalf("%q exited %d: %s", args, code, stderr.String())
			}
			if patched == nil || patched.MinInstances == nil || *patched.MinInstances != 0 {
				t.Fatalf("PATCH min_instances = %+v, want 0", patched)
			}
			if patched.ScalingPolicy == nil || patched.ScalingPolicy.MinInstances != 0 {
				t.Fatalf("PATCH scaling_policy = %+v, want min_instances 0 beside the explicit --min 0", patched.ScalingPolicy)
			}
		})
	}
}
