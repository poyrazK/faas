package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDeploymentAuditReturnsDecodedEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/deployments/deployment-id/audit" || r.URL.Query().Get("limit") != "50" {
			t.Error("incorrect request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"at":"2026-10-12T18:00:00Z","kind":"deploy.mcp_admission","actor":"apid:mcp_traffic_admission","data":{"mcp_admission":{"version":1,"outcome":"rejected"}}}],"limit":50}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "operator")
	history, err := client.ListDeploymentAudit(t.Context(), "deployment-id", 50)
	if err != nil || history.Limit != 50 || len(history.Items) != 1 || history.Items[0].Kind != "deploy.mcp_admission" {
		t.Fatalf("decoded audit missing: %+v %v", history, err)
	}
}
