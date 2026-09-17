package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetPreviewStatusReturnsPreviewAndLatestDeployment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/pr-42-web":
			_ = json.NewEncoder(w).Encode(AppResponse{Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42})
		case "/v1/apps/pr-42-web/deployments/latest":
			_ = json.NewEncoder(w).Encode(DeploymentResponse{ID: "deploy-42", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "token").GetPreviewStatus(context.Background(), "pr-42-web")
	if err != nil {
		t.Fatal(err)
	}
	if got.App.PreviewPRNumber != 42 || got.LatestDeployment == nil || got.LatestDeployment.ID != "deploy-42" {
		t.Fatalf("status = %+v", got)
	}
}

func TestGetPreviewStatusAllowsPreviewWithoutDeployment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/pr-42-web" {
			_ = json.NewEncoder(w).Encode(AppResponse{Slug: "pr-42-web", PreviewOfSlug: "web"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","status":404}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "token").GetPreviewStatus(context.Background(), "pr-42-web")
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestDeployment != nil {
		t.Fatalf("latest deployment = %+v, want nil", got.LatestDeployment)
	}
}

func TestGetPreviewStatusRejectsProductionApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AppResponse{Slug: "web"})
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, "token").GetPreviewStatus(context.Background(), "web"); err == nil {
		t.Fatal("GetPreviewStatus succeeded for a production app")
	}
}
