package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectApplyInitialSecretsMultipartContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects" || r.Header.Get("Idempotency-Key") != "project-action" {
			t.Error("wrong operation or retry identity")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		var values ProjectInitialSecrets
		if err := json.Unmarshal([]byte(r.FormValue("initial_secrets")), &values); err != nil {
			t.Fatal(err)
		}
		if values["api"]["DATABASE_URL"] != "request-only" || r.FormValue("only") != "api" || r.FormValue("no_triggers") != "true" {
			t.Error("configuration or selection was dropped")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"project_id":"project"}`))
	}))
	defer server.Close()
	ctx := ContextWithIdempotencyKey(context.Background(), "project-action")
	_, err := NewClient(server.URL, "test-key").ApplyProjectPlanWithInitialSecrets(ctx, "reviewed", strings.NewReader("archive"), "source.tar.gz", "shop", "", "main", 0, []string{"api"}, nil, false, true, "", "", ProjectInitialSecrets{"api": {"DATABASE_URL": "request-only"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyCachedProjectPlanJSONContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/apply-plan" || r.Method != http.MethodPost {
			t.Error("wrong cached apply route")
		}
		var request ApplyCachedProjectPlanRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.PlanToken != "reviewed" || len(request.Only) != 1 || request.Only[0] != "api" {
			t.Error("reviewed source or selection missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"project_id":"project"}`))
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "test-key").ApplyCachedProjectPlan(context.Background(), ApplyCachedProjectPlanRequest{PlanToken: "reviewed", Only: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
}
