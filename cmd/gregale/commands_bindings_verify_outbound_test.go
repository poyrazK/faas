// adr: 430 — CLI requires exact candidate receipts and sanitized supported probe results.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdBindingsOutboundProbePolicyAndCandidateVerification(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, failure := range []string{"", "failed response", "wrong integration", "wrong deployment", "missing stage", "truncated"} {
			t.Run(failure+map[bool]string{true: " all", false: " single"}[all], func(t *testing.T) {
				id, dep := uuid.NewString(), uuid.NewString()
				configured, created := false, 0
				policy := api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == "DELETE" && r.URL.Path == "/v1/apps/api/tasks/task":
						_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task", Status: api.AppTaskStatusCancelled})
					case r.Method == "PUT" && r.URL.Path == "/v1/outbound/integrations/"+id+"/probe-policy":
						var got api.OutboundBindingProbePolicy
						if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got != policy {
							t.Fatalf("policy: %+v %v", got, err)
						}
						configured = true
						_ = json.NewEncoder(w).Encode(policy)
					case r.Method == "GET" && r.URL.Path == "/v1/apps/api/bindings":
						if r.URL.Query().Get("deployment_id") != dep {
							t.Error("missing exact selector")
						}
						_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: "api", Complete: true, RequestedDeploymentID: dep, VerificationDeploymentID: dep, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeOutbound, Name: "payments", Scope: "app", State: "enabled", OutboundProbe: &policy}, {Type: api.BindingTypeOutbound, Name: "other", Scope: "app", State: "enabled"}}})
					case r.Method == "GET" && r.URL.Path == "/v1/apps/api/outbound-bindings":
						_ = json.NewEncoder(w).Encode(api.OutboundAppBindingList{Items: []api.OutboundAppBinding{{Integration: api.OutboundIntegrationOffer{ID: id, Name: "payments"}}}})
					case r.Method == "POST" && r.URL.Path == "/v1/apps/api/tasks":
						created++
						var request api.CreateAppTaskRequest
						_ = json.NewDecoder(r.Body).Decode(&request)
						if len(request.Command) != 2 || request.Command[0] != api.AppTaskOutboundBindingProbeCommand || request.Command[1] != id || request.VerificationDeploymentID != dep {
							t.Fatalf("wrong admission: %+v", request)
						}
						check := api.OutboundBindingProbeCheck{Status: "passed", Detail: "PRIVATE_DETAIL"}
						report := api.OutboundBindingProbeReport{IntegrationID: id, Configuration: check, Identity: check, Gateway: check, Response: check}
						task := api.AppTaskResponse{ID: "task", Status: api.AppTaskStatusSucceeded, DeploymentID: dep, DeploymentScope: "production", ExitCode: new(int)}
						switch failure {
						case "failed response":
							report.Response.Status = "failed"
							report.Error = "PRIVATE_PROVIDER_ERROR"
						case "wrong integration":
							report.IntegrationID = "PRIVATE_ID"
						case "wrong deployment":
							task.DeploymentID = uuid.NewString()
						case "missing stage":
							report.Identity.Status = ""
						case "truncated":
							task.OutputTruncated = true
						}
						raw, _ := json.Marshal(report)
						task.StdoutTail = string(raw)
						_ = json.NewEncoder(w).Encode(task)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				var out, stderr bytes.Buffer
				oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
				osStdout, osStderr, jsonOutput = &out, &stderr, true
				t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
				if code := run([]string{"bindings", "probe-policy", id, "--path", "/health"}); code != 0 || !configured {
					t.Fatalf("configure: %d %s", code, out.String())
				}
				out.Reset()
				args := []string{"bindings", "verify", "api", "--deployment", dep, "--poll-interval", "1ms"}
				if all {
					args = append(args, "--all")
				} else {
					args = append(args, "--outbound", id)
				}
				code := run(args)
				if (code == 0) != (failure == "") || created != 1 || strings.Contains(out.String(), "PRIVATE_") {
					t.Fatalf("exit=%d created=%d output=%s %s", code, created, out.String(), stderr.String())
				}
			})
		}
	}
}
