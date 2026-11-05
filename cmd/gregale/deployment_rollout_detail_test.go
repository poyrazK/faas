package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A safe rollout stopped by a release gate stays "live" at 0% traffic while
// its predecessor serves; `gregale deployment` printed only "status: live"
// (Hunt 12 H12-35).
func TestDeploymentDetailShowsTrafficAndRolloutStop(t *testing.T) {
	const id = "d60fae6bc65b4b6da73de76cf713ab97"
	tests := []struct {
		name   string
		dep    api.DeploymentResponse
		want   []string
		reject []string
	}{
		{
			name: "gate stopped the rollout",
			dep:  api.DeploymentResponse{ID: id, Status: "live", TrafficPercent: 0, RolloutState: "aborted", RolloutAbortedReason: "circuit breaker: a release gate failed; restoring predecessor 412dc8a3"},
			want: []string{"traffic:       0%\n", "rollout:       aborted\n", "rollout_stop:  circuit breaker: a release gate failed; restoring predecessor 412dc8a3\n"},
		},
		{
			name:   "serving release",
			dep:    api.DeploymentResponse{ID: id, Status: "live", TrafficPercent: 100, RolloutState: "complete"},
			want:   []string{"traffic:       100%\n", "rollout:       complete\n"},
			reject: []string{"rollout_stop:"},
		},
		{
			name:   "failed build",
			dep:    api.DeploymentResponse{ID: id, Status: "failed"},
			reject: []string{"traffic:", "rollout:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/deployments/"+id {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(tt.dep)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.CmdDeploymentGet([]string{id})
			restore()
			got := out.String()
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, got)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q:\n%s", w, got)
				}
			}
			for _, r := range tt.reject {
				if strings.Contains(got, r) {
					t.Errorf("unexpected %q:\n%s", r, got)
				}
			}
		})
	}
}
