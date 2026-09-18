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

func TestClientCronLifecycleUsesPublicContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/crons":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("cron create request did not include an idempotency key")
			}
			var request cronRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if request.AppID != "app-1" || request.Schedule != "*/15 * * * *" || request.Path != "/internal/sync" || request.Timezone != "UTC" {
				t.Fatalf("create request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"id":"cron-1","app_id":"app-1","schedule":"*/15 * * * *","path":"/internal/sync","enabled":true,"timezone":"UTC","skip_if_running":true,"created_at":"2026-09-18T10:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/crons/cron-1":
			_, _ = w.Write([]byte(`{"id":"cron-1","app_id":"app-1","schedule":"*/15 * * * *","path":"/internal/sync","enabled":true,"timezone":"UTC","skip_if_running":true,"created_at":"2026-09-18T10:00:00Z","last_fired_at":"2026-09-18T10:15:00Z"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/crons/cron-1":
			var patch cronPatch
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatalf("decode update request: %v", err)
			}
			if patch.Schedule == nil || *patch.Schedule != "0 * * * *" {
				t.Fatalf("update request = %+v", patch)
			}
			_, _ = w.Write([]byte(`{"id":"cron-1","app_id":"app-1","schedule":"0 * * * *","path":"/internal/sync","enabled":true,"timezone":"UTC","skip_if_running":true,"created_at":"2026-09-18T10:00:00Z"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/crons/cron-1":
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
	skipIfRunning := true
	created, err := client.createCron(context.Background(), cronRequest{
		AppID:         "app-1",
		Schedule:      "*/15 * * * *",
		Path:          "/internal/sync",
		Timezone:      "UTC",
		SkipIfRunning: &skipIfRunning,
	})
	if err != nil {
		t.Fatalf("createCron: %v", err)
	}
	if created.ID != "cron-1" || created.AppID != "app-1" {
		t.Fatalf("create response = %+v", created)
	}

	read, err := client.getCron(context.Background(), "cron-1")
	if err != nil {
		t.Fatalf("getCron: %v", err)
	}
	if read.LastFiredAt != "2026-09-18T10:15:00Z" {
		t.Fatalf("read response = %+v", read)
	}

	updatedSchedule := "0 * * * *"
	updated, err := client.updateCron(context.Background(), "cron-1", cronPatch{Schedule: &updatedSchedule})
	if err != nil {
		t.Fatalf("updateCron: %v", err)
	}
	if updated.Schedule != "0 * * * *" {
		t.Fatalf("update response = %+v", updated)
	}

	if err := client.deleteCron(context.Background(), "cron-1"); err != nil {
		t.Fatalf("deleteCron: %v", err)
	}
}

func TestClientSecretLifecycleUsesWriteOnlyValueAndScopedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/apps/orders/secrets/API_TOKEN":
			if r.URL.Query().Get("scope") != "production" {
				t.Fatalf("secret write scope = %q, want production", r.URL.Query().Get("scope"))
			}
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("secret write request did not include an idempotency key")
			}
			var request secretRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode secret write request: %v", err)
			}
			if request.Value != "super-secret" {
				t.Fatalf("secret write value = %q", request.Value)
			}
			_, _ = w.Write([]byte(`{"key":"API_TOKEN"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/orders/secrets":
			if r.URL.Query().Get("scope") != "production" {
				t.Fatalf("secret read scope = %q, want production", r.URL.Query().Get("scope"))
			}
			_, _ = w.Write([]byte(`{"secrets":[{"key":"API_TOKEN","scope":"production","created_at":"2026-09-18T10:00:00Z","updated_at":"2026-09-18T10:05:00Z","kid":"age-1","value_hash":"hash-1"}],"quota_max":25,"count":1}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/orders/secrets/API_TOKEN":
			if r.URL.Query().Get("scope") != "production" {
				t.Fatalf("secret delete scope = %q, want production", r.URL.Query().Get("scope"))
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if err := client.setSecret(context.Background(), "orders", "production", "API_TOKEN", "super-secret"); err != nil {
		t.Fatalf("setSecret: %v", err)
	}
	metadata, found, err := client.getSecret(context.Background(), "orders", "production", "API_TOKEN")
	if err != nil {
		t.Fatalf("getSecret: %v", err)
	}
	if !found || metadata.Scope != "production" || metadata.ValueHash != "hash-1" {
		t.Fatalf("metadata = %+v, found = %v", metadata, found)
	}
	if err := client.deleteSecret(context.Background(), "orders", "production", "API_TOKEN"); err != nil {
		t.Fatalf("deleteSecret: %v", err)
	}
}

func TestClientEnvLifecycleUsesWriteOnlyValueAndScopedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/apps/orders/env/LOG_LEVEL":
			if r.URL.Query().Get("scope") != "production" {
				t.Fatalf("env write scope = %q, want production", r.URL.Query().Get("scope"))
			}
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("env write request did not include an idempotency key")
			}
			var request envRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode env write request: %v", err)
			}
			if request.Value != "debug" {
				t.Fatalf("env write value = %q", request.Value)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/orders/env":
			if r.URL.Query().Get("scope") != "production" {
				t.Fatalf("env read scope = %q, want production", r.URL.Query().Get("scope"))
			}
			_, _ = w.Write([]byte(`{"env":[{"key":"LOG_LEVEL","created_at":"2026-09-18T10:00:00Z","updated_at":"2026-09-18T10:05:00Z"}],"quota_max":50,"count":1}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/orders/env/LOG_LEVEL":
			if r.URL.Query().Get("scope") != "production" {
				t.Fatalf("env delete scope = %q, want production", r.URL.Query().Get("scope"))
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if err := client.setEnv(context.Background(), "orders", "production", "LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("setEnv: %v", err)
	}
	metadata, found, err := client.getEnv(context.Background(), "orders", "production", "LOG_LEVEL")
	if err != nil {
		t.Fatalf("getEnv: %v", err)
	}
	if !found || metadata.Scope != "production" || metadata.UpdatedAt != "2026-09-18T10:05:00Z" {
		t.Fatalf("metadata = %+v, found = %v", metadata, found)
	}
	if err := client.deleteEnv(context.Background(), "orders", "production", "LOG_LEVEL"); err != nil {
		t.Fatalf("deleteEnv: %v", err)
	}
}

func TestClientDeploymentLifecycleUsesSourceRefAndPreviewMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/orders/deployments/source-ref":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("deployment create request did not include an idempotency key")
			}
			var request deploymentRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode deployment request: %v", err)
			}
			if request.Repo != "acme/orders" || request.Ref != "main" || request.Environment != "production" || request.NoTriggers {
				t.Fatalf("deployment request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"id":"dep-1","app_id":"app-1","status":"pending","created_at":"2026-09-18T10:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/dep-1":
			_, _ = w.Write([]byte(`{"id":"dep-1","app_id":"app-1","build_id":"build-1","kind":"github","status":"live","image_digest":"sha256:abc","created_at":"2026-09-18T10:00:00Z","source_url":"https://github.com/acme/orders","commit_sha":"abc123","scope":"production","stage_state":{"readiness":"complete"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/dep-1/url":
			_, _ = w.Write([]byte(`{"deployment_id":"dep-1","host":"deploy-1-orders.gregale.dev","url":"https://deploy-1-orders.gregale.dev","alive":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/orders/deployments/dep-1/cancel":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	created, err := client.createSourceRefDeployment(context.Background(), "orders", deploymentRequest{
		Repo:        "acme/orders",
		Ref:         "main",
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("createSourceRefDeployment: %v", err)
	}
	if created.ID != "dep-1" || created.Status != "pending" {
		t.Fatalf("created deployment = %+v", created)
	}
	read, err := client.getDeployment(context.Background(), "dep-1")
	if err != nil {
		t.Fatalf("getDeployment: %v", err)
	}
	if read.Status != "live" || read.CommitSHA != "abc123" || read.Scope != "production" {
		t.Fatalf("deployment = %+v", read)
	}
	preview, err := client.getDeploymentURL(context.Background(), "dep-1")
	if err != nil {
		t.Fatalf("getDeploymentURL: %v", err)
	}
	if !preview.Alive || preview.URL == "" {
		t.Fatalf("preview = %+v", preview)
	}
}
