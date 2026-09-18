package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientCreateAppUsesPublicContractAndIdempotency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps" {
			t.Fatalf("request = %s %s, want POST /v1/apps", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("create request did not include an idempotency key")
		}
		var request appRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Slug != "orders" || request.Type != "app" {
			t.Fatalf("request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"app-1","slug":"orders","status":"active","url":"https://orders.gregale.dev"}`))
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	got, err := client.createApp(context.Background(), appRequest{Slug: "orders", Type: "app"})
	if err != nil {
		t.Fatalf("createApp: %v", err)
	}
	if got.ID != "app-1" || got.URL != "https://orders.gregale.dev" {
		t.Fatalf("response = %+v", got)
	}
}

func TestClientProblemErrorIsActionableWithoutEchoingBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"plan_limit","title":"Plan limit","detail":"raise the plan"}`))
	}))
	defer server.Close()

	client, err := newClient(server.URL, "secret-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	_, err = client.getApp(context.Background(), "orders")
	if err == nil {
		t.Fatal("getApp succeeded, want API error")
	}
	if !strings.Contains(err.Error(), "plan_limit") || !strings.Contains(err.Error(), "raise the plan") {
		t.Fatalf("error = %q, want problem code and detail", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error echoed bearer token: %q", err)
	}
}

func TestNewClientRejectsUnsupportedBaseURL(t *testing.T) {
	if _, err := newClient("file:///tmp/gregale", "token"); err == nil {
		t.Fatal("newClient accepted unsupported scheme")
	}
}
