package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientGetAppsSlugPolicyStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/policy/status" || r.URL.Query().Get("wait") != "2s" {
			t.Errorf("request = %s %s, want GET /v1/apps/demo/policy/status?wait=2s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"app_id":"app-1","desired_revision":42,"state":"active","coverage":["gateway_app_cache","deployment_traffic"],"serving_gateways":1,"applied_gateways":1,"pending_gateways":0,"stale_gateways":0,"edge_rules":{"desired_revision":17,"state":"pending","serving_gateways":1,"applied_gateways":0,"pending_gateways":1,"stale_gateways":0}}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-token")
	client.SetCompletionCache(nil)
	status, err := client.GetAppsSlugPolicyStatus(context.Background(), "demo", 2*time.Second)
	if err != nil {
		t.Fatalf("GetAppsSlugPolicyStatus: %v", err)
	}
	if status.DesiredRevision != 42 || status.State != "active" || status.AppliedGateways != 1 ||
		status.EdgeRules.DesiredRevision != 17 || status.EdgeRules.State != "pending" || status.EdgeRules.PendingGateways != 1 {
		t.Fatalf("status = %+v, want active revision 42", status)
	}
	if _, err := client.GetAppsSlugPolicyStatus(context.Background(), "demo", 11*time.Second); err == nil {
		t.Fatal("wait above server maximum was accepted")
	}
}
