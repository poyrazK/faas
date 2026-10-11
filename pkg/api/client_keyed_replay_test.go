package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReplayKeyedInvocationClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/invocations/parent/replay-keyed" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"child","status_url":"/v1/invocations/child"}`))
	}))
	defer server.Close()
	response, err := NewClient(server.URL, "token").ReplayKeyedInvocation(context.Background(), "parent")
	if err != nil || response.ID != "child" || response.StatusURL != "/v1/invocations/child" {
		t.Fatalf("replay response: %+v %v", response, err)
	}
}
