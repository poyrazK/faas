package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestAlertRollbackStatusUsesGETAndPreservesPair(t *testing.T) {
	receipt := AlertRollback{ID: "3e9f323a-ade6-442b-8444-c91da107fe44", RuleID: "a2b9cc53-907f-4b5c-88a4-fd0c21214556", AppID: "142b7504-f03a-4ee2-aeb3-14d922a845d4", Scope: "default", CandidateDeploymentID: "5b87c415-7c93-4932-acab-a3c90e98be86", PredecessorDeploymentID: "6b87c415-7c93-4932-acab-a3c90e98be86", Status: "blocked", FiredAt: time.Now(), UpdatedAt: time.Now(), Blockers: []BindingCheckFinding{{Code: "binding_verification_missing", Message: "verify predecessor"}}}
	receipt.Service, receipt.ServiceRequestID, receipt.ServicePhase = true, receipt.ID, "pending"
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet {
			t.Errorf("mutating status method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/api/alert-rollbacks" {
			_ = json.NewEncoder(w).Encode([]AlertRollback{receipt})
		} else {
			if r.URL.Path != "/v1/apps/api/alert-rollbacks/"+receipt.ID {
				t.Errorf("wrong path %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(receipt)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.ListAlertRollbacks(context.Background(), "api")
	if err != nil || len(rows) != 1 {
		t.Fatalf("list %+v %v", rows, err)
	}
	got, err := client.GetAlertRollback(context.Background(), "api", receipt.ID)
	if err != nil || got.CandidateDeploymentID != receipt.CandidateDeploymentID || got.PredecessorDeploymentID != receipt.PredecessorDeploymentID || got.Status != "blocked" || len(got.Blockers) != 1 || reads != 2 || !got.Service || got.ServiceRequestID != receipt.ID || got.ServicePhase != "pending" {
		t.Fatalf("receipt %+v %v reads=%d", got, err, reads)
	}
}

func TestHistoricalAlertRollbackWire(t *testing.T) {
	original := AlertRollback{ID: "3e9f323a-ade6-442b-8444-c91da107fe44", Historical: true, RollbackOperationID: "3e9f323a-ade6-442b-8444-c91da107fe44", RollbackPhase: "preparing"}
	original.DeploymentEvidence = &AlertRollbackDeploymentEvidence{Version: 1, DeploymentID: original.ID, Metric: "error_rate_pct", Comparison: "gt", Threshold: 1, WindowSpec: "5m", Requests: 20, ServerErrors: 2, MinimumRequests: 20, ErrorRatePct: 10, Status: "breached"}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var got AlertRollback
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Historical || got.RollbackOperationID != original.ID || got.RollbackPhase != "preparing" || !reflect.DeepEqual(got.DeploymentEvidence, original.DeploymentEvidence) {
		t.Fatalf("receipt %+v", got)
	}
}
