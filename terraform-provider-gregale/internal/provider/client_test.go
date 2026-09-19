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

func TestClientGetAppUsesSlugLookupContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/orders-api" {
			t.Fatalf("request = %s %s, want GET /v1/apps/orders-api", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Fatal("app lookup unexpectedly included an idempotency key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"app-1","slug":"orders-api","type":"app","visibility":"private","runtime":"node24","resource_profile":"small","ram_mb":512,"max_concurrency":8,"idle_timeout_s":60,"health_path":"/healthz","health_path_wakes":true,"status":"active","url":"https://orders-api.gregale.dev"}`))
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	got, err := client.getApp(context.Background(), "orders-api")
	if err != nil {
		t.Fatalf("getApp: %v", err)
	}
	if got.ID != "app-1" || got.Slug != "orders-api" || got.Runtime != "node24" || got.RAMMB == nil || *got.RAMMB != 512 || got.URL == "" {
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

func TestClientCreateImageDeploymentUsesPublicContractAndIdempotency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/orders/deployments" {
			t.Fatalf("request = %s %s, want POST /v1/apps/orders/deployments", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("image deployment request did not include an idempotency key")
		}
		var request imageDeploymentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode image deployment request: %v", err)
		}
		if request.Image != "ghcr.io/acme/orders@sha256:abc123" || request.Environment != "production" {
			t.Fatalf("image deployment request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dep-oci","app_id":"app-1","kind":"oci","status":"pending","created_at":"2026-09-19T10:00:00Z"}`))
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	got, err := client.createImageDeployment(context.Background(), "orders", imageDeploymentRequest{
		Image:       "ghcr.io/acme/orders@sha256:abc123",
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("createImageDeployment: %v", err)
	}
	if got.ID != "dep-oci" || got.AppID != "app-1" || got.Kind != "oci" || got.Status != "pending" {
		t.Fatalf("created image deployment = %+v", got)
	}
}

func TestClientGetLatestAppDeploymentUsesAppScopedContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/orders/deployments/latest" {
			t.Fatalf("request = %s %s, want GET /v1/apps/orders/deployments/latest", r.Method, r.URL.RequestURI())
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Fatal("latest deployment lookup included an idempotency key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dep-latest","app_id":"app-1","kind":"github","status":"live","commit_sha":"abc123","created_at":"2026-09-18T10:00:00Z"}`))
	}))
	defer server.Close()

	client, err := newClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	got, err := client.getLatestAppDeployment(context.Background(), "orders")
	if err != nil {
		t.Fatalf("getLatestAppDeployment: %v", err)
	}
	if got.ID != "dep-latest" || got.AppID != "app-1" || got.Status != "live" || got.CommitSHA != "abc123" {
		t.Fatalf("latest deployment = %+v", got)
	}
}

func TestClientTCPListenerLifecycleUsesPublicContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/orders/tcp-listeners":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("TCP listener lookup included an idempotency key")
			}
			_, _ = w.Write([]byte(`[{"id":"listener-1","name":"postgres","guest_port":5432,"public_port":41001,"protocol":"tcp","enabled":true,"created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:00:00Z"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/orders/tcp-listeners":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("TCP listener create request did not include an idempotency key")
			}
			var request tcpListenerRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode TCP listener create request: %v", err)
			}
			if request.Name != "postgres" || request.GuestPort != 5432 || request.PublicPort == nil || *request.PublicPort != 41001 {
				t.Fatalf("TCP listener create request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"id":"listener-1","name":"postgres","guest_port":5432,"public_port":41001,"protocol":"tcp","enabled":true,"created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:00:00Z"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/orders/tcp-listeners/postgres":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("TCP listener update request included an idempotency key")
			}
			var update tcpListenerUpdate
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Fatalf("decode TCP listener update request: %v", err)
			}
			if update.Enabled == nil || *update.Enabled {
				t.Fatalf("TCP listener update request = %+v", update)
			}
			_, _ = w.Write([]byte(`{"id":"listener-1","name":"postgres","guest_port":5432,"public_port":41001,"protocol":"tcp","enabled":false,"created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:01:00Z"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/orders/tcp-listeners/postgres":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("TCP listener delete request included an idempotency key")
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
	publicPort := 41001
	created, err := client.createTCPListener(context.Background(), "orders", tcpListenerRequest{
		Name:       "postgres",
		GuestPort:  5432,
		PublicPort: &publicPort,
	})
	if err != nil {
		t.Fatalf("createTCPListener: %v", err)
	}
	if created.ID != "listener-1" || created.PublicPort != 41001 || !created.Enabled {
		t.Fatalf("created listener = %+v", created)
	}

	listed, err := client.listTCPListeners(context.Background(), "orders")
	if err != nil {
		t.Fatalf("listTCPListeners: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "postgres" {
		t.Fatalf("listed listeners = %+v", listed)
	}

	disabled := false
	updated, err := client.updateTCPListener(context.Background(), "orders", "postgres", tcpListenerUpdate{Enabled: &disabled})
	if err != nil {
		t.Fatalf("updateTCPListener: %v", err)
	}
	if updated.Enabled {
		t.Fatalf("updated listener = %+v", updated)
	}
	if err := client.deleteTCPListener(context.Background(), "orders", "postgres"); err != nil {
		t.Fatalf("deleteTCPListener: %v", err)
	}
}

func TestClientStaticEgressIPLifecycleUsesPublicContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/orders/static-egress-ip":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("static egress IP lookup included an idempotency key")
			}
			_, _ = w.Write([]byte(`{"ip":"203.0.113.42","set_at":"2026-09-19T10:00:00Z","plan_cap":1,"plan_allowed":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/apps/orders/static-egress-ip":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("static egress IP set request did not include an idempotency key")
			}
			var request staticEgressIPRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode static egress IP set request: %v", err)
			}
			if request.IP != "203.0.113.42" || !request.Set {
				t.Fatalf("static egress IP set request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"ip":"203.0.113.42","set_at":"2026-09-19T10:00:00Z","plan_cap":1,"plan_allowed":true}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/orders/static-egress-ip":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("static egress IP clear request included an idempotency key")
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
	created, err := client.setStaticEgressIP(context.Background(), "orders", "203.0.113.42")
	if err != nil {
		t.Fatalf("setStaticEgressIP: %v", err)
	}
	if created.IP == nil || *created.IP != "203.0.113.42" || !created.PlanAllowed {
		t.Fatalf("created static egress IP = %+v", created)
	}

	read, err := client.getStaticEgressIP(context.Background(), "orders")
	if err != nil {
		t.Fatalf("getStaticEgressIP: %v", err)
	}
	if read.IP == nil || *read.IP != "203.0.113.42" || read.PlanCap != 1 {
		t.Fatalf("read static egress IP = %+v", read)
	}

	if err := client.clearStaticEgressIP(context.Background(), "orders"); err != nil {
		t.Fatalf("clearStaticEgressIP: %v", err)
	}
}

func TestClientPrivateNetworkAttachmentLifecycleUsesPublicContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/orders/network/private":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("private-network attachment lookup included an idempotency key")
			}
			_, _ = w.Write([]byte(`{"feature_enabled":true,"plan_allowed":true,"max_cidrs":16,"attachment":{"id":"attachment-1","network_id":"prod-vpc","region":"fra1","cidrs":["10.30.0.0/16"],"allowed_cidrs":["10.30.0.0/24"],"address":"10.30.0.10","status":"ready","status_detail":"connected","created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:01:00Z"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/apps/orders/network/private":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("private-network attachment set request did not include an idempotency key")
			}
			var request privateNetworkAttachmentRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode private-network attachment request: %v", err)
			}
			if request.NetworkID != "prod-vpc" || request.Region != "fra1" || len(request.CIDRs) != 1 || request.CIDRs[0] != "10.30.0.0/16" || len(request.AllowedCIDRs) != 1 || request.AllowedCIDRs[0] != "10.30.0.0/24" {
				t.Fatalf("private-network attachment request = %+v", request)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"feature_enabled":true,"plan_allowed":true,"max_cidrs":16,"attachment":{"id":"attachment-1","network_id":"prod-vpc","region":"fra1","cidrs":["10.30.0.0/16"],"allowed_cidrs":["10.30.0.0/24"],"address":"10.30.0.10","status":"pending","status_detail":"waiting for connector","created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:01:00Z"}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/orders/network/private":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("private-network attachment clear request included an idempotency key")
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
	created, err := client.setPrivateNetworkAttachment(context.Background(), "orders", privateNetworkAttachmentRequest{
		NetworkID: "prod-vpc", Region: "fra1", CIDRs: []string{"10.30.0.0/16"}, AllowedCIDRs: []string{"10.30.0.0/24"},
	})
	if err != nil {
		t.Fatalf("setPrivateNetworkAttachment: %v", err)
	}
	if created.Attachment == nil || created.Attachment.Status != "pending" || created.Attachment.NetworkID != "prod-vpc" {
		t.Fatalf("created attachment = %+v", created)
	}

	read, err := client.getPrivateNetworkAttachment(context.Background(), "orders")
	if err != nil {
		t.Fatalf("getPrivateNetworkAttachment: %v", err)
	}
	if read.Attachment == nil || read.Attachment.Status != "ready" || read.Attachment.Address != "10.30.0.10" || !read.PlanAllowed {
		t.Fatalf("read attachment = %+v", read)
	}

	if err := client.clearPrivateNetworkAttachment(context.Background(), "orders"); err != nil {
		t.Fatalf("clearPrivateNetworkAttachment: %v", err)
	}
}

func TestClientPrivateNetworkLifecycleUsesPublicContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/networks":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("private network create request did not include an idempotency key")
			}
			var request privateNetworkRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode private network create request: %v", err)
			}
			if request.Name != "production" || request.Region != "fra1" || request.CIDR != "10.20.0.0/16" {
				t.Fatalf("private network create request = %+v", request)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"prod-vpc","name":"production","region":"fra1","cidr":"10.20.0.0/16","status":"ready","status_detail":"network is ready","created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:01:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/networks/prod-vpc":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("private network lookup included an idempotency key")
			}
			_, _ = w.Write([]byte(`{"id":"prod-vpc","name":"production","region":"fra1","cidr":"10.20.0.0/16","status":"ready","status_detail":"network is ready","created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:01:00Z"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/networks/prod-vpc":
			if r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("private network delete request included an idempotency key")
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
	created, err := client.createPrivateNetwork(context.Background(), privateNetworkRequest{Name: "production", Region: "fra1", CIDR: "10.20.0.0/16"})
	if err != nil {
		t.Fatalf("createPrivateNetwork: %v", err)
	}
	if created.ID != "prod-vpc" || created.Status != "ready" {
		t.Fatalf("created private network = %+v", created)
	}

	read, err := client.getPrivateNetwork(context.Background(), "prod-vpc")
	if err != nil {
		t.Fatalf("getPrivateNetwork: %v", err)
	}
	if read.CIDR != "10.20.0.0/16" || read.Region != "fra1" {
		t.Fatalf("read private network = %+v", read)
	}

	if err := client.deletePrivateNetwork(context.Background(), "prod-vpc"); err != nil {
		t.Fatalf("deletePrivateNetwork: %v", err)
	}
}
