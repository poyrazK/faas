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

func TestClientCreateDomainUsesPublicContractAndIdempotency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/domains" {
			t.Fatalf("request = %s %s, want POST /v1/domains", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("domain create request did not include an idempotency key")
		}
		var request domainRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Domain != "api.example.com" || request.AppID != "app-1" {
			t.Fatalf("request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domain":"api.example.com","app_id":"app-1","challenge_token":"challenge","txt_record":"gregale=challenge","verified":false,"cert_status":"pending"}`))
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	got, err := client.createDomain(context.Background(), domainRequest{Domain: "api.example.com", AppID: "app-1"})
	if err != nil {
		t.Fatalf("createDomain: %v", err)
	}
	if got.Domain != "api.example.com" || got.AppID != "app-1" || got.CertStatus != "pending" {
		t.Fatalf("response = %+v", got)
	}
}

func TestClientAlertRuleLifecycleUsesPublicContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/orders/alerts":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("alert create request did not include an idempotency key")
			}
			var request alertRuleRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if request.Name != "latency" || request.Metric != "latency_p95_ms" || request.WebhookSecret != "secret-value" {
				t.Fatalf("create request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"id":"alert-1","app_id":"app-1","name":"latency","enabled":true,"metric":"latency_p95_ms","comparison":"gt","threshold":500,"window_spec":"5m","action":"webhook","webhook_url":"https://hooks.example.test/gregale","webhook_secret_sealed_masked":"***","cooldown_minutes":15,"state":"ok"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/orders/alerts/alert-1":
			_, _ = w.Write([]byte(`{"id":"alert-1","app_id":"app-1","name":"latency","enabled":true,"metric":"latency_p95_ms","comparison":"gt","threshold":500,"window_spec":"5m","action":"webhook","webhook_url":"https://hooks.example.test/gregale","webhook_secret_sealed_masked":"***","cooldown_minutes":15,"state":"firing"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/orders/alerts/alert-1":
			var patch alertRulePatch
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatalf("decode update request: %v", err)
			}
			if patch.Name == nil || *patch.Name != "latency-critical" {
				t.Fatalf("update request = %+v", patch)
			}
			_, _ = w.Write([]byte(`{"id":"alert-1","app_id":"app-1","name":"latency-critical","enabled":true,"metric":"latency_p95_ms","comparison":"gt","threshold":500,"window_spec":"5m","action":"webhook","webhook_url":"https://hooks.example.test/gregale","webhook_secret_sealed_masked":"***","cooldown_minutes":15,"state":"firing"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/orders/alerts/alert-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	created, err := client.createAlertRule(context.Background(), "orders", alertRuleRequest{
		Name:          "latency",
		Metric:        "latency_p95_ms",
		Comparison:    "gt",
		Threshold:     500,
		WindowSpec:    "5m",
		WebhookURL:    "https://hooks.example.test/gregale",
		WebhookSecret: "secret-value",
	})
	if err != nil {
		t.Fatalf("createAlertRule: %v", err)
	}
	if created.ID != "alert-1" || created.WebhookSecretSealedMasked != "***" {
		t.Fatalf("create response = %+v", created)
	}

	read, err := client.getAlertRule(context.Background(), "orders", "alert-1")
	if err != nil {
		t.Fatalf("getAlertRule: %v", err)
	}
	if read.State != "firing" {
		t.Fatalf("read response = %+v", read)
	}

	updatedName := "latency-critical"
	updated, err := client.updateAlertRule(context.Background(), "orders", "alert-1", alertRulePatch{
		Name: &updatedName,
	})
	if err != nil {
		t.Fatalf("updateAlertRule: %v", err)
	}
	if updated.Name != "latency-critical" {
		t.Fatalf("update response = %+v", updated)
	}

	if err := client.deleteAlertRule(context.Background(), "orders", "alert-1"); err != nil {
		t.Fatalf("deleteAlertRule: %v", err)
	}
}
