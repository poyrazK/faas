package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBindingPromotionClientPreservesStructuredGateReceiptAndProblems(t *testing.T) {
	const candidate = "01234567-89ab-cdef-0123-456789abcdef"
	const serving = "fedcba98-7654-3210-fedc-ba9876543210"
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt", true: "problem"}[blocked], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments/"+candidate+"/promote" {
					t.Errorf("request: %s %s", r.Method, r.URL)
				}
				var req BindingPromotionRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.ExpectedServingDeploymentID == nil || *req.ExpectedServingDeploymentID != serving || req.MaxVerificationAge != "5m" || !req.AllowUnsupported {
					t.Errorf("policy: %+v", req)
				}
				report := BindingCheckReport{Passed: !blocked, DeploymentID: candidate, ExpectedDeploymentID: candidate, Scope: "default", MaxVerificationAge: "5m0s", AllowUnsupported: true, Coverage: "partial"}
				if blocked {
					report.Blockers = []BindingCheckFinding{{Code: "verification_expired", Message: "Verify the candidate."}}
					problem := NewProblem(409, "bindings_check_changed", "Bindings changed", "Verify the candidate.")
					problem.BindingsCheck = &report
					WriteProblem(w, problem)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(BindingPromotionResponse{Deployment: DeploymentResponse{ID: candidate, TrafficPercent: 100}, FromPercent: 0, ToPercent: 100, BindingsCheck: &report})
			}))
			defer srv.Close()
			expected := serving
			got, err := NewClient(srv.URL, "test").PromoteDeploymentWithBindings(context.Background(), candidate, BindingPromotionRequest{ExpectedServingDeploymentID: &expected, MaxVerificationAge: "5m", AllowUnsupported: true})
			if blocked {
				var apiError *APIError
				if !errors.As(err, &apiError) || apiError.Problem.BindingsCheck == nil || apiError.Problem.BindingsCheck.Blockers[0].Code != "verification_expired" {
					t.Fatalf("structured problem lost: %v", err)
				}
			} else if err != nil || got.BindingsCheck == nil || !got.BindingsCheck.Passed || got.Deployment.ID != candidate || got.ToPercent != 100 {
				t.Fatalf("receipt lost: %+v %v", got, err)
			}
		})
	}
}

func TestBindingPromotionApplicationAckClientUsesDedicatedRouteWithoutFallback(t *testing.T) {
	for _, oldServer := range []bool{false, true} {
		t.Run(map[bool]string{false: "supported", true: "old_server"}[oldServer], func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments/candidate/promote-with-application-ack" {
					t.Errorf("unsafe fallback: %s %s", r.Method, r.URL)
				}
				if oldServer {
					http.NotFound(w, r)
					return
				}
				var request BindingPromotionRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.RequireApplicationAck {
					t.Errorf("policy: %+v %v", request, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(BindingPromotionResponse{BindingsCheck: &BindingCheckReport{Passed: true, RequireApplicationAck: true}})
			}))
			defer server.Close()
			receipt, err := NewClient(server.URL, "test").PromoteDeploymentWithBindings(context.Background(), "candidate", BindingPromotionRequest{RequireApplicationAck: true})
			if posts != 1 || oldServer && err == nil || !oldServer && (err != nil || receipt.BindingsCheck == nil || !receipt.BindingsCheck.RequireApplicationAck) {
				t.Fatalf("old=%t posts=%d receipt=%+v err=%v", oldServer, posts, receipt, err)
			}
		})
	}
}
