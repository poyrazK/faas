package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDeploymentCompanionsIsAnExactRead(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/deployments/reviewed%2Fid/companions" || r.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("unexpected read=%s %s", r.Method, r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"account_id":"account-a","app_id":"app-a","app_slug":"my-app","deployment_id":"reviewed/id","scope":"default","state":"available","companions":[],"unexpected_secret":"never-retain"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	result, err := client.GetDeploymentCompanions(context.Background(), "reviewed/id")
	if err != nil || result.AccountID != "account-a" || result.DeploymentID != "reviewed/id" || result.State != "available" || result.Companions == nil {
		t.Fatalf("read=%+v %v", result, err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestClientDeploymentCompanionsHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled read reached server") }))
	defer server.Close()
	client := NewClient(server.URL, "test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetDeploymentCompanions(ctx, "reviewed-id"); err == nil {
		t.Fatal("cancelled read accepted")
	}
}
