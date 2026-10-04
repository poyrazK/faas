package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrafficPromotionBindingsUsesGateRouteEvenForIdempotentRetry(t *testing.T) {
	const target = "0123456789abcdef0123456789abcdef"
	const serving = "fedcba9876543210fedcba9876543210"
	for _, percent := range []int{0, 100} {
		t.Run(string(rune('a'+percent)), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/"+serving:
					writeJSONTest(w, api.DeploymentResponse{ID: serving, Status: statusLive, TrafficPercent: 100})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/"+target:
					writeJSONTest(w, api.DeploymentResponse{ID: target, Status: statusLive, TrafficPercent: percent})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/deployments/"+target+"/promote":
					calls++
					var req api.BindingPromotionRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.ExpectedServingDeploymentID == nil || *req.ExpectedServingDeploymentID != serving || req.MaxVerificationAge != "2m0s" || !req.AllowUnsupported {
						t.Errorf("policy: %+v", req)
					}
					writeJSONTest(w, api.BindingPromotionResponse{Deployment: api.DeploymentResponse{ID: target, Status: statusLive, TrafficPercent: 100}, FromPercent: percent, ToPercent: 100, AlreadyPromoted: percent == 100, BindingsCheck: &api.BindingCheckReport{Passed: true, DeploymentID: target, ExpectedDeploymentID: target, Scope: "default", CheckedAt: time.Now(), MaxVerificationAge: "2m0s", AllowUnsupported: true, Coverage: "partial"}})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 404)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			var out, errs bytes.Buffer
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &out, &errs, true
			defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
			code := cmdTrafficPromote([]string{"--deployment", target, "--if-serving", serving, "--require-bindings", "--max-verification-age", "2m", "--allow-unsupported"})
			if code != 0 || calls != 1 {
				t.Fatalf("code=%d gate calls=%d output=%s %s", code, calls, out.String(), errs.String())
			}
			var receipt api.BindingPromotionResponse
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.AlreadyPromoted != (percent == 100) || receipt.BindingsCheck == nil {
				t.Fatalf("receipt: %+v %v", receipt, err)
			}
		})
	}
}

func TestTrafficPromotionBindingsRejectsOldServersAndUnconfirmedReports(t *testing.T) {
	const target = "0123456789abcdef0123456789abcdef"
	for _, mode := range []string{"old_server", "missing_report", "wrong_deployment", "failed_report", "changed_policy", "wrong_scope", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeJSONTest(w, api.DeploymentResponse{ID: target, Status: statusLive})
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments/"+target+"/promote" {
					t.Errorf("unsafe fallback: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				posts++
				if mode == "old_server" {
					http.NotFound(w, r)
					return
				}
				report := &api.BindingCheckReport{Passed: true, DeploymentID: target, ExpectedDeploymentID: target, Scope: "default", CheckedAt: time.Now(), MaxVerificationAge: "10m0s"}
				switch mode {
				case "missing_report":
					report = nil
				case "wrong_deployment":
					report.DeploymentID = "fedcba9876543210fedcba9876543210"
				case "failed_report":
					report.Passed = false
				case "changed_policy":
					report.AllowUnsupported = true
				case "wrong_scope":
					report.Scope = "staging"
				case "blocked":
					report.Passed = false
					report.Blockers = []api.BindingCheckFinding{{Code: "verification_stale", Message: "Verify the candidate."}}
					problem := api.NewProblem(409, "bindings_check_failed", "Bindings check failed", "Verify the candidate.")
					problem.BindingsCheck = report
					api.WriteProblem(w, problem)
					return
				}
				writeJSONTest(w, api.BindingPromotionResponse{Deployment: api.DeploymentResponse{ID: target, TrafficPercent: 100}, ToPercent: 100, BindingsCheck: report})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			var out, errs bytes.Buffer
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &out, &errs, true
			defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
			captured, restoreErr := captureStderr(t)
			code := cmdTrafficPromote([]string{"--deployment", target, "--require-bindings"})
			restoreErr()
			if code == 0 || posts != 1 || out.Len() != 0 {
				t.Fatalf("unconfirmed gate accepted: code=%d posts=%d out=%s", code, posts, out.String())
			}
			if mode == "blocked" && !strings.Contains(captured.String(), "verification_stale") {
				t.Fatalf("structured blockers lost: %s", captured.String())
			}
		})
	}
}

func TestTrafficPromotionBindingPolicyRequiresGateBeforeNetwork(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	for _, flags := range [][]string{{"--max-verification-age", "1m"}, {"--allow-unsupported"}, {"--require-application-ack"}, {"--require-bindings", "--max-verification-age", "0s"}} {
		if code := cmdTrafficPromote(append([]string{"--deployment", "0123456789abcdef0123456789abcdef"}, flags...)); code == 0 {
			t.Fatalf("invalid policy accepted: %v", flags)
		}
	}
}

func TestTrafficPromotionApplicationAckUsesDedicatedRouteAndRequiresPolicyEcho(t *testing.T) {
	const target = "0123456789abcdef0123456789abcdef"
	for _, mode := range []string{"supported", "old_server", "missing_echo"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeJSONTest(w, api.DeploymentResponse{ID: target, Status: statusLive})
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments/"+target+"/promote-with-application-ack" {
					t.Errorf("unsafe fallback: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				posts++
				var request api.BindingPromotionRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.RequireApplicationAck {
					t.Errorf("policy: %+v %v", request, err)
				}
				if mode == "old_server" {
					http.NotFound(w, r)
					return
				}
				report := &api.BindingCheckReport{Passed: true, DeploymentID: target, ExpectedDeploymentID: target, Scope: "default", CheckedAt: time.Now(), MaxVerificationAge: "10m0s", RequireApplicationAck: mode == "supported"}
				writeJSONTest(w, api.BindingPromotionResponse{Deployment: api.DeploymentResponse{ID: target, TrafficPercent: 100}, ToPercent: 100, BindingsCheck: report})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var out, errs bytes.Buffer
			osStdout, osStderr, jsonOutput = &out, &errs, true
			defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
			code := cmdTrafficPromote([]string{"--deployment", target, "--require-bindings", "--require-application-ack"})
			if posts != 1 || (code == 0) != (mode == "supported") {
				t.Fatalf("%s: code=%d posts=%d %s %s", mode, code, posts, out.String(), errs.String())
			}
		})
	}
}
