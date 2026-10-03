package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_AccountReleaseWebhookRoutes(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	const did = "fedcba9876543210fedcba9876543210"
	type observed struct {
		method, path string
		body         map[string]any
	}
	requests := make(chan observed, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests <- observed{r.Method, r.URL.RequestURI(), body}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			if r.URL.Path == "/v1/account/release-webhooks" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			fallthrough
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "token")
	ctx := context.Background()
	check := func(method, path string) observed {
		t.Helper()
		got := <-requests
		if got.method != method || got.path != path {
			t.Fatalf("route = %s %s, want %s %s", got.method, got.path, method, path)
		}
		return got
	}
	if _, err := c.ListAccountReleaseWebhooks(ctx); err != nil {
		t.Fatal(err)
	}
	check(http.MethodGet, "/v1/account/release-webhooks")
	if _, err := c.CreateAccountReleaseWebhook(ctx, CreateAccountReleaseWebhookRequest{
		TargetURL: "https://example.com/releases", WebhookSecret: "shh", EventFilter: []string{"deployment.live"},
	}); err != nil {
		t.Fatal(err)
	}
	if body := check(http.MethodPost, "/v1/account/release-webhooks").body; body["webhook_secret"] != "shh" {
		t.Fatalf("create body = %v", body)
	}
	if _, err := c.GetAccountReleaseWebhook(ctx, id); err != nil {
		t.Fatal(err)
	}
	check(http.MethodGet, "/v1/account/release-webhooks/"+id)
	if _, err := c.UpdateAccountReleaseWebhook(ctx, id, UpdateAccountReleaseWebhookRequest{}); err != nil {
		t.Fatal(err)
	}
	check(http.MethodPatch, "/v1/account/release-webhooks/"+id)
	if _, err := c.RotateAccountReleaseWebhookSecret(ctx, id, RotateAppWebhookSecretRequest{WebhookSecret: "new"}); err != nil {
		t.Fatal(err)
	}
	check(http.MethodPost, "/v1/account/release-webhooks/"+id+"/rotate-secret")
	if _, err := c.ListAccountReleaseWebhookDeliveries(ctx, id, ListAppWebhookDeliveriesOptions{PageSize: 20, PageToken: "cursor"}); err != nil {
		t.Fatal(err)
	}
	check(http.MethodGet, "/v1/account/release-webhooks/"+id+"/deliveries?page_size=20&page_token=cursor")
	if _, err := c.RetryAccountReleaseWebhookDelivery(ctx, id, did); err != nil {
		t.Fatal(err)
	}
	check(http.MethodPost, "/v1/account/release-webhooks/"+id+"/deliveries/"+did+"/retry")
	if err := c.DeleteAccountReleaseWebhook(ctx, id); err != nil {
		t.Fatal(err)
	}
	check(http.MethodDelete, "/v1/account/release-webhooks/"+id)
}
