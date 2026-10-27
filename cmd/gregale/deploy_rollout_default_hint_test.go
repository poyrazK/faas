package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: `gregale deploy --rollout fast` ended with "A
// health-gated rollout is this app's default", calling the user's explicit
// choice the default (Hunt 14 H14-29).
func TestRolloutDefaultHintOnlyForTheDefaultRollout(t *testing.T) {
	for _, tc := range []struct {
		preset   string
		wantHint bool
	}{
		{api.FastReleaseCanaryPreset, false},
		{api.DefaultReleaseCanaryPreset, true},
	} {
		t.Run(tc.preset, func(t *testing.T) {
			dep := api.DeploymentResponse{ID: "d1", AppID: "a1", Status: main.StatusLive, CanaryPreset: tc.preset,
				CanaryStep: 0, CanaryTotalSteps: 3, RolloutState: "rolling_out", TrafficPercent: 5}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/deployments/d1":
					_ = json.NewEncoder(w).Encode(dep)
				case "/v1/apps/my-app":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app", ReleasePolicy: api.ReleasePolicySafe})
				default:
					http.Error(w, "not found", http.StatusNotFound)
				}
			}))
			defer srv.Close()
			var out bytes.Buffer
			oldOut := *main.OsStdout
			*main.OsStdout = &out
			defer func() { *main.OsStdout = oldOut }()
			main.RenderSuccessfulDeployment(context.Background(), api.NewClient(srv.URL, "fp_live_x"), dep, "my-app")
			if got := strings.Contains(out.String(), "is this app's default"); got != tc.wantHint {
				t.Fatalf("default-rollout hint shown = %v, want %v:\n%s", got, tc.wantHint, out.String())
			}
		})
	}
}
