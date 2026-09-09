package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAppDeploymentSummaryUsesAppScopedRoute(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/myapp/deployments/dep-1/summary" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deployment":{"id":"dep-1"},"changes":[],"rollback_target_id":"dep-0"}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "fp_test").GetAppDeploymentSummary(context.Background(), "myapp", "dep-1")
	if err != nil {
		t.Fatal(err)
	}
	if !called || got.Deployment.ID != "dep-1" || got.RollbackTargetID != "dep-0" {
		t.Errorf("called=%v response=%+v", called, got)
	}
}
