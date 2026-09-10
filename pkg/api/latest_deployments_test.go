package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListLatestDeploymentsByApp_UsesBatchRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.RequestURI() != "/v1/deployments/latest-by-app" {
			t.Errorf("RequestURI = %q, want /v1/deployments/latest-by-app", r.URL.RequestURI())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"dep-latest","app_id":"app-one","status":"live"}]}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "fp_test").ListLatestDeploymentsByApp(context.Background())
	if err != nil {
		t.Fatalf("ListLatestDeploymentsByApp: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "dep-latest" || got.Items[0].AppID != "app-one" {
		t.Fatalf("decoded response = %+v, want deployment dep-latest for app-one", got)
	}
}
