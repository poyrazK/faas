// adr: 20261022203523
package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func runPerInstanceConcurrency(command string, args []string) int {
	if command == "app" {
		return main.CmdApp(append([]string{"hello"}, args...))
	}
	return main.CmdAppScale("hello", args)
}

// TestPerInstanceConcurrencyFlagMergesIntoThePolicy pins that
// --per-instance-concurrency on `gregale app` and `gregale app scale` sends
// the value inside the app's current scaling policy, keeping its other fields.
func TestPerInstanceConcurrencyFlagMergesIntoThePolicy(t *testing.T) {
	for _, tc := range []struct {
		command string
		value   string
		want    int
	}{
		{command: "app", value: "2", want: 2},
		{command: "scale", value: "2", want: 2},
		{command: "scale", value: "0", want: 0},
	} {
		t.Run(tc.command+"="+tc.value, func(t *testing.T) {
			main.ResetJSONOut(t)
			(*main.JsonOutput) = true
			var writes int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					main.WriteJSONTest(w, api.AppResponse{Slug: "hello",
						ScalingPolicy: &api.ScalingPolicy{MaxInstances: 3, ScaleInCooldownS: 73, PerInstanceConcurrency: 5}})
					return
				}
				writes++
				var req api.UpdateAppRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.ScalingPolicy == nil || req.ScalingPolicy.PerInstanceConcurrency != tc.want ||
					req.ScalingPolicy.MaxInstances != 3 || req.ScalingPolicy.ScaleInCooldownS != 73 {
					t.Errorf("policy = %+v, want per_instance_concurrency %d with max_instances 3 and scale_in_cooldown_s 73 kept", req.ScalingPolicy, tc.want)
				}
				main.WriteJSONTest(w, api.AppResponse{Slug: "hello"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			if code := runPerInstanceConcurrency(tc.command, []string{"--per-instance-concurrency", tc.value}); code != 0 || writes != 1 {
				t.Fatalf("exit %d, writes %d; want 0 and 1", code, writes)
			}
		})
	}
}

func TestPerInstanceConcurrencyFlagRejectsNegativeBeforeAnyRequest(t *testing.T) {
	for _, command := range []string{"app", "scale"} {
		t.Run(command, func(t *testing.T) {
			main.ResetJSONOut(t)
			(*main.JsonOutput) = true
			var requests int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				main.WriteJSONTest(w, api.AppResponse{Slug: "hello"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			if code := runPerInstanceConcurrency(command, []string{"--per-instance-concurrency", "-1"}); code == 0 || requests != 0 {
				t.Fatalf("exit %d after %d requests; want a non-zero exit before any request", code, requests)
			}
		})
	}
}
