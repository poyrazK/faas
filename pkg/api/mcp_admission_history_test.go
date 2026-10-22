package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPAdmissionHistoryClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("mutating method")
		}
		if r.URL.Path == "/v1/mcp/admissions/receipt" {
			_, _ = w.Write([]byte(`{"version":1,"id":"receipt","outcome":"accepted"}`))
			return
		}
		if r.URL.Path != "/v1/deployments/deployment/mcp-admissions" || r.URL.Query().Get("cursor") != "42" || r.URL.Query().Get("outcome") != "rejected" {
			t.Errorf("unexpected URL: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"older","outcome":"rejected"}],"next_cursor":"17","limit":1}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	receipt, err := client.GetMCPAdmissionReceipt(context.Background(), "receipt")
	if err != nil || receipt.ID != "receipt" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	page, err := client.ListMCPAdmissionHistory(context.Background(), "deployment", "42", "rejected", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor != "17" || page.Limit != 1 {
		t.Fatalf("page: %+v %v", page, err)
	}
}

func TestMCPAdmissionExplanationIdentityReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected mutation")
		}
		if r.URL.Path == "/v1/deployments/deployment" {
			_, _ = w.Write([]byte(`{"id":"deployment","app_id":"app"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"app","slug":"reports"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "operator")
	deployment, err := client.GetDeployment(context.Background(), "deployment")
	if err != nil || deployment.ID != "deployment" || deployment.AppID != "app" {
		t.Fatalf("deployment identity: %+v %v", deployment, err)
	}
	app, err := client.GetApp(context.Background(), "reports")
	if err != nil || app.ID != "app" || app.Slug != "reports" {
		t.Fatalf("app identity: %+v %v", app, err)
	}
}
