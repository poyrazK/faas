package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusClientMethodsUsePublicAndAdminContracts(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/status" {
			t.Fatalf("overview request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(PublicStatusOverview{})
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.URL, "token")
	client.SetCompletionCache(nil)
	if _, err := client.GetStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := client.PostAdminStatusIncidents(context.Background(), AdminStatusEventCreateRequest{}, "status-create-1")
	var problem *Problem
	if !errors.As(err, &problem) || problem.Code != CodeUnsupportedByCLI {
		t.Fatalf("admin status SDK error = %v, want unsupported_by_cli", err)
	}
	if calls != 1 {
		t.Fatalf("admin cookie-only method reached HTTP server; calls=%d", calls)
	}
}
