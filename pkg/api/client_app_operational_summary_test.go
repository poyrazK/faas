package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAppOperationalSummaryPreservesUnknownAndBlockedRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/myapp/operational-summary" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":1,"app_id":"app-id","monitoring":{"available":true,"status":"unknown","reason":"insufficient_requests"},"recovery":{"rollbacks_available":true,"rollbacks":[{"id":"rollback-id","status":"blocked"}],"restarts_available":false,"restarts":[]},"recommendations":[]}`))
	}))
	t.Cleanup(server.Close)
	summary, err := NewClient(server.URL, "fp_test").GetAppOperationalSummary(t.Context(), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Monitoring.Status != "unknown" || summary.Recovery.RestartsAvailable || len(summary.Recovery.Rollbacks) != 1 || summary.Recovery.Rollbacks[0].Status != "blocked" {
		t.Fatalf("operational facts changed on decode: %+v", summary)
	}
}
