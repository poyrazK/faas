// adr: 531
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppEnvironmentFlagScopesConfigurationReadsAndWrites(t *testing.T) {
	for _, command := range []string{"app", "scale"} {
		t.Run(command, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			var reads, writes int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/hello" || r.URL.Query().Get("environment") != "staging" {
					t.Errorf("request escaped selected environment: %s %s", r.Method, r.URL)
				}
				if r.Method == http.MethodGet {
					reads++
					w.Header().Set("X-Gregale-Workload-Revision", "7")
					writeJSONTest(w, api.AppResponse{Slug: "hello", MinInstances: 2,
						ScalingPolicy: &api.ScalingPolicy{MinInstances: 2, MaxInstances: 8, ScaleInCooldownS: 73}})
					return
				}
				writes++
				if r.Header.Get("If-Workload-Revision") != "7" {
					t.Errorf("configuration merge omitted its observed revision: %v", r.Header)
				}
				var req api.UpdateAppRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.ScalingPolicy == nil || req.ScalingPolicy.MaxInstances != 8 || req.ScalingPolicy.ScaleInCooldownS != 73 || req.ScalingPolicy.ConcurrencyOverflow != api.ConcurrencyOverflowDrop {
					t.Errorf("stage policy was not preserved during merge: %+v", req.ScalingPolicy)
				}
				writeJSONTest(w, api.AppResponse{Slug: "hello"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			args := []string{"--environment", "staging", "--concurrency-overflow", "drop"}
			var code int
			if command == "app" {
				code = cmdApp(append([]string{"hello"}, args...))
			} else {
				code = cmdAppScale("hello", args)
			}
			if code != 0 || reads != 1 || writes != 1 {
				t.Fatalf("command result=%d reads=%d writes=%d", code, reads, writes)
			}
		})
	}
}
