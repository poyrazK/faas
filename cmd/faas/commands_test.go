package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSanitizeSlug(t *testing.T) {
	tests := map[string]string{
		"MyApp":       "myapp",
		"my_api":      "my-api",
		"hello world": "hello-world",
		"a":           "app-a",
		"--weird--":   "weird",
		"Node.JS_App": "node-js-app",
	}
	for in, want := range tests {
		if got := sanitizeSlug(in); got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClientRendersAPIProblem(t *testing.T) {
	// The client must surface the server's RFC 7807 problem as an APIError with
	// the docs URL, not a generic error (UX §3.3).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteProblem(w, api.ErrPlanLimitApps(api.MustLimitsFor(api.PlanFree), 1))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp_live_x")
	_, err := c.CreateApp(context.Background(), api.CreateAppRequest{Slug: "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want *APIError, got %T", err)
	}
	if ae.Problem.Code != api.CodePlanLimitApps {
		t.Errorf("code = %q", ae.Problem.Code)
	}
	if exitCodeForStatus(ae.Problem.Status) != 1 {
		t.Errorf("403 should map to exit 1, got %d", exitCodeForStatus(ae.Problem.Status))
	}
}

func TestClientAuthAndDeploySucceed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			writeJSONTest(w, api.AccountResponse{Email: "a@b.c", Plan: "pro"})
		case "/v1/apps/hello/deployments":
			writeJSONTest(w, api.DeploymentResponse{ID: "d1", Status: "pending"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp_live_x")
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	dep, err := c.Deploy(context.Background(), "hello", api.CreateDeploymentRequest{Image: "r@sha256:x"})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.Status != "pending" {
		t.Errorf("status = %q", dep.Status)
	}
}

func writeJSONTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
