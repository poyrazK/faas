package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetAppDebugRunningBuildsQuery(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DebugRunningResponse{AppID: "app-1", Since: "6h"})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	response, err := client.GetAppDebugRunningWithLimit(t.Context(), "my-app", "6h", 5)
	if err != nil {
		t.Fatalf("GetAppDebugRunningWithLimit: %v", err)
	}
	if response.AppID != "app-1" {
		t.Fatalf("response = %+v", response)
	}
	if got.URL.Path != "/v1/apps/my-app/debug/running" {
		t.Fatalf("path = %q, want /v1/apps/my-app/debug/running", got.URL.Path)
	}
	if got.URL.Query().Get("since") != "6h" || got.URL.Query().Get("limit") != "5" {
		t.Fatalf("query = %s, want since=6h&limit=5", got.URL.RawQuery)
	}
}
