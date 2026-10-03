package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueImpactAlertPolicyClient(t *testing.T) {
	var configured int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/exports/issue-impact-alert-policy" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			var request UpdateIssueImpactAlertPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode policy: %v", err)
			}
			configured = request.MinimumCustomers
		} else if r.Method != http.MethodGet {
			t.Errorf("request method = %q", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(IssueImpactAlertPolicy{Enabled: configured > 0, MinimumCustomers: configured, WindowSeconds: 86400})
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-token")
	set, err := client.SetIssueImpactAlertPolicy(context.Background(), "exports", UpdateIssueImpactAlertPolicyRequest{MinimumCustomers: 5})
	if err != nil || !set.Enabled || set.MinimumCustomers != 5 {
		t.Fatalf("set policy = %+v, %v", set, err)
	}
	got, err := client.GetIssueImpactAlertPolicy(context.Background(), "exports")
	if err != nil || !got.Enabled || got.MinimumCustomers != 5 || got.WindowSeconds != 86400 {
		t.Fatalf("get policy = %+v, %v", got, err)
	}
}
