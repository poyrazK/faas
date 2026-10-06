package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckedRollbackAcceptsExactPairAndReadsOperation(t *testing.T) {
	target, current, request := "a2b9cc53-907f-4b5c-88a4-fd0c21214556", "5b87c415-7c93-4932-acab-a3c90e98be86", "3e9f323a-ade6-442b-8444-c91da107fe44"
	operation := RollbackOperation{ID: request, AppID: "142b7504-f03a-4ee2-aeb3-14d922a845d4", Scope: "default", TargetDeploymentID: target, CurrentDeploymentID: current, Status: "preparing", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var req RollbackRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TargetDeploymentID == nil || *req.TargetDeploymentID != target || req.ExpectedCurrentDeploymentID == nil || *req.ExpectedCurrentDeploymentID != current {
				t.Errorf("wrong selection: %+v %v", req, err)
			}
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing idempotency key")
			}
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(DeploymentResponse{ID: target, AppID: operation.AppID, RollbackOperation: &operation})
		} else {
			if r.URL.Path != "/v1/apps/api/rollbacks/"+request {
				t.Errorf("wrong operation path: %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(operation)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := client.CheckedRollback(context.Background(), "api", RollbackRequest{TargetDeploymentID: &target, ExpectedCurrentDeploymentID: &current})
	if err != nil || accepted.RollbackOperation == nil || accepted.RollbackOperation.ID != request {
		t.Fatalf("accepted receipt: %+v %v", accepted, err)
	}
	read, err := client.GetRollbackOperation(context.Background(), "api", request)
	if err != nil || read.TargetDeploymentID != target || read.CurrentDeploymentID != current || read.Status != "preparing" {
		t.Fatalf("operation: %+v %v", read, err)
	}
}
