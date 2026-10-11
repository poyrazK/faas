package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverCanaryRolloutPinsPairAndDecodesReceipt(t *testing.T) {
	const candidate = "a2b9cc53-907f-4b5c-88a4-fd0c21214556"
	const predecessor = "5b87c415-7c93-4932-acab-a3c90e98be86"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/api/rollouts/recover" || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		var req RecoverRolloutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Action != "abort" || req.DeploymentID != candidate || req.ExpectedPredecessorDeploymentID != predecessor || req.Reason != "incident" {
			t.Errorf("selection: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deployment":{"id":"` + candidate + `","rollout_state":"aborted"},"audit_id":"42","recovery":{"deployment_id":"` + candidate + `","predecessor_deployment_id":"` + predecessor + `","restored_traffic_percent":100,"bindings_checks":[{"deployment_id":"` + predecessor + `","passed":true}]}}`))
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.RecoverCanaryRollout(context.Background(), "api", candidate, predecessor, "incident")
	if err != nil || resp.Deployment.ID != candidate || resp.AuditID != "42" || resp.Recovery == nil || resp.Recovery.PredecessorDeploymentID != predecessor || len(resp.Recovery.BindingsChecks) != 1 || !resp.Recovery.BindingsChecks[0].Passed {
		t.Fatalf("receipt: %+v %v", resp, err)
	}
}

func TestRecoverExactServiceRolloutAcceptanceAndStatus(t *testing.T) {
	const candidate = "a2b9cc53-907f-4b5c-88a4-fd0c21214556"
	const predecessor = "5b87c415-7c93-4932-acab-a3c90e98be86"
	const requestID = "3e9f323a-ade6-442b-8444-c91da107fe44"
	gate := ServiceRolloutBindingGate{RequestID: requestID, Action: "abort", DeploymentID: predecessor, Status: "pending"}
	handoff := ServiceRolloutHandoffResponse{Action: "abort", Phase: "pending", PredecessorDeploymentID: predecessor, BindingsCheck: &gate}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/v1/apps/api/rollouts/recover" {
			var req RecoverRolloutRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.DeploymentID != candidate || req.ExpectedPredecessorDeploymentID != predecessor {
				t.Errorf("wrong pins: %+v", req)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"deployment": map[string]any{"id": candidate, "app_id": "app", "rollout_state": "rolling_out", "service_rollout_handoff": handoff}, "audit_id": "42", "service_recovery": ServiceRolloutRecoveryReceipt{DeploymentID: candidate, PredecessorDeploymentID: predecessor, RequestID: requestID, Status: "accepted"}})
		} else if r.Method == "GET" && r.URL.Path == "/v1/deployments/"+candidate {
			handoff.Phase = "routing"
			gate.Status = "passed"
			gate.AuditID = "43"
			_ = json.NewEncoder(w).Encode(map[string]any{"id": candidate, "app_id": "app", "rollout_state": "rolling_out", "service_rollout_handoff": handoff})
		} else {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	receipt, err := client.RecoverExactRollout(ctx, "api", candidate, predecessor, "incident")
	if err != nil || receipt.ServiceRecovery == nil || receipt.Recovery != nil || receipt.ServiceRecovery.RequestID != requestID || receipt.Deployment.RolloutState != "rolling_out" || receipt.Deployment.ServiceRolloutHandoff.BindingsCheck.Status != "pending" {
		t.Fatalf("acceptance: %+v %v", receipt, err)
	}
	status, err := client.GetDeployment(ctx, candidate)
	if err != nil || status.ServiceRolloutHandoff.Phase != "routing" || status.ServiceRolloutHandoff.BindingsCheck.AuditID != "43" || status.RolloutState != "rolling_out" {
		t.Fatalf("status: %+v %v", status, err)
	}
}
