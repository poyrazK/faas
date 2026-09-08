package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetLatestAppDeploymentUsesAppScopedRoute(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/myapp/deployments/latest" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"0123456789abcdef0123456789abcdef","app_id":"abcdef0123456789abcdef0123456789"}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "fp_test").GetLatestAppDeployment(context.Background(), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if !called || got.ID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("called=%v response=%+v", called, got)
	}
}
