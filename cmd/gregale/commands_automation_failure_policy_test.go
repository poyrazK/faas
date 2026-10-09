// adr: 830
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationFailurePolicyCLIConfigurationAndResume(t *testing.T) {
	for _, mode := range []string{"status", "configure", "resume", "stale_resume"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			output := captureAutomationStdout(t)
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				if r.Header.Get("Authorization") != "Bearer failure-token" {
					t.Error("missing auth")
				}
				base := "/v1/apps/billing/automations/invoice/failure-policy"
				if r.URL.Path != base && r.URL.Path != base+"/resume" {
					t.Errorf("wrong route %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				out := api.AutomationFailurePolicyResponse{Policy: api.AutomationFailurePolicy{Version: 2, Enabled: true, FailureThreshold: 3, MinCompletedRuns: 5, WindowSeconds: 300}, Paused: true, Generation: 7, PendingRuns: 2, RetainedEvents: 3, History: []api.AutomationFailureTransition{}}
				if r.Method == http.MethodPut {
					var body api.SetAutomationFailurePolicyRequest
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedVersion != 2 || body.Enabled == nil || !*body.Enabled || body.FailureThreshold != 4 || body.MinCompletedRuns != 10 || body.WindowSeconds != 600 {
						t.Errorf("bad config %+v %v", body, err)
					}
				}
				if r.Method == http.MethodPost {
					var body api.ResumeAutomationFailurePauseRequest
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedGeneration != 7 {
						t.Errorf("bad resume %+v %v", body, err)
					}
					out.Paused = false
					out.Generation = 8
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "failure-token")
			args := []string{"--app", "billing", "--name", "invoice"}
			code := 0
			switch mode {
			case "status":
				code = cmdAutomationsFailurePolicy(args)
			case "configure":
				code = cmdAutomationsFailurePolicy(append(args, "--enabled", "--expected-version", "2", "--failure-threshold", "4", "--min-completed-runs", "10", "--window-seconds", "600"))
			case "resume":
				code = cmdAutomationsFailureResume(append(args, "--expected-generation", "7"))
			case "stale_resume":
				code = cmdAutomationsFailureResume(append(args, "--expected-generation", "6"))
			}
			if mode == "stale_resume" {
				if code == 0 || len(methods) != 1 {
					t.Fatalf("stale resume wrote: code=%d methods=%v", code, methods)
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit=%d", code)
			}
			var got api.AutomationFailurePolicyResponse
			if err := json.Unmarshal(output.Bytes(), &got); err != nil || got.RetainedEvents != 3 {
				t.Fatalf("lost preview %s %v", output.String(), err)
			}
			if mode == "resume" && (strings.Join(methods, ",") != "GET,POST" || got.Paused) {
				t.Fatalf("resume missing preview or state update: %v %+v", methods, got)
			}
		})
	}
}
func TestAutomationFailurePolicyCLIRequiresExplicitConfigurationIntent(t *testing.T) {
	for _, args := range [][]string{{"--app", "billing", "--name", "invoice", "--expected-version", "1"}, {"--app", "billing", "--name", "invoice", "--enabled"}, {"--app", "billing", "--name", "invoice", "--enabled", "--expected-version", "1", "--window-seconds", "59"}} {
		if code := cmdAutomationsFailurePolicy(args); code == 0 {
			t.Fatalf("invalid request accepted: %v", args)
		}
	}
}
