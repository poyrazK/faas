package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func testBackend() managedpostgres.BackendConfig {
	return managedpostgres.BackendConfig{
		ID:        "neon-eu",
		Driver:    "neon",
		Region:    "eu-central-1",
		Namespace: "org-gregale-12345678",
		Settings: map[string]string{
			settingRegionID:         "aws-eu-central-1",
			settingDatabaseName:     "gregale",
			settingMaxStorageBytes:  "107374182400",
			settingMaxRestoreWindow: "604800",
		},
		SecretEnv: map[string]string{apiKeySecret: "TEST_NEON_API_KEY"},
	}
}

func testDatabaseSpec() managedpostgres.Spec {
	return managedpostgres.Spec{
		Region: "eu-central-1", PostgresMajor: 17,
		Class: managedpostgres.ClassBurstable, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 10 << 30, RestoreWindowSeconds: 86400,
	}
}

func testProvider(t *testing.T, handler http.Handler) *Provider {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL + "/api/v2")
	if err != nil {
		t.Fatal(err)
	}
	p := newProvider("eu-central-1", "org-gregale-12345678", "secret-api-key", baseURL, server.Client(), settings{
		regionID: "aws-eu-central-1", databaseName: "gregale",
		maxStorageBytes: 100 << 30, maxRestoreWindow: 604800,
	})
	p.credentialPollInterval = time.Millisecond
	return p
}

func writeResponse(t *testing.T, writer http.ResponseWriter, status int, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if value != nil {
		if err := json.NewEncoder(writer).Encode(value); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}
}

func TestNewValidatesBackendAndAdvertisesConservativeCapabilities(t *testing.T) {
	config := testBackend()
	provider, err := New(config, func(string) string { return "secret" })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	capabilities := provider.Capabilities()
	if capabilities.MaxStorageBytes != 100<<30 || capabilities.MaxRestoreWindowSeconds != 604800 || len(capabilities.Availability) != 1 || capabilities.Availability[0] != managedpostgres.AvailabilitySingleZone {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	if err := capabilities.Supports(testDatabaseSpec()); err != nil {
		t.Fatalf("expected test spec support: %v", err)
	}
	ha := testDatabaseSpec()
	ha.Availability = managedpostgres.AvailabilityHighlyAvailable
	if !errors.Is(capabilities.Supports(ha), managedpostgres.ErrUnsupported) {
		t.Fatal("Neon adapter advertised an unqualified HA promise")
	}

	tests := map[string]func(*managedpostgres.BackendConfig){
		"organization":    func(config *managedpostgres.BackendConfig) { config.Namespace = "personal" },
		"secret mapping":  func(config *managedpostgres.BackendConfig) { config.SecretEnv = nil },
		"missing secret":  func(config *managedpostgres.BackendConfig) {},
		"physical region": func(config *managedpostgres.BackendConfig) { config.Settings[settingRegionID] = "eu-central-1" },
		"storage cap":     func(config *managedpostgres.BackendConfig) { delete(config.Settings, settingMaxStorageBytes) },
		"restore cap":     func(config *managedpostgres.BackendConfig) { config.Settings[settingMaxRestoreWindow] = "2592001" },
		"unknown setting": func(config *managedpostgres.BackendConfig) { config.Settings["api_url"] = "https://attacker.test" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := testBackend()
			mutate(&candidate)
			getenv := func(string) string { return "secret" }
			if name == "missing secret" {
				getenv = func(string) string { return "" }
			}
			if _, err := New(candidate, getenv); err == nil || strings.Contains(err.Error(), "secret-api-key") {
				t.Fatalf("New error = %v", err)
			}
		})
	}
}

func TestProvisionCreatesDeterministicProjectAndPersistsIDBeforePolling(t *testing.T) {
	var postCount atomic.Int32
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret-api-key" {
			t.Errorf("authorization header missing")
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v2/projects":
			writeResponse(t, writer, http.StatusOK, map[string]any{"projects": []any{}, "pagination": map[string]any{}})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v2/projects":
			postCount.Add(1)
			var payload createProjectRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode create: %v", err)
			}
			if payload.Project.OrganizationID != "org-gregale-12345678" || payload.Project.RegionID != "aws-eu-central-1" || payload.Project.PostgresMajor != 17 || payload.Project.Settings.Quota.LogicalSizeBytes == nil || *payload.Project.Settings.Quota.LogicalSizeBytes != 10<<30 || !payload.Project.StorePasswords {
				t.Errorf("create payload = %+v", payload.Project)
			}
			writeResponse(t, writer, http.StatusCreated, map[string]any{"project": map[string]any{"id": "silent-snow-12345678", "name": payload.Project.Name}, "operations": []map[string]any{{"id": "operation-1", "status": "running"}}})
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
			writeResponse(t, writer, http.StatusNotFound, map[string]any{"message": "missing"})
		}
	}))
	request := managedpostgres.ProvisionRequest{ResourceID: "11111111-1111-1111-1111-111111111111", IdempotencyKey: "provision-11111111-1111-1111-1111-111111111111", Spec: testDatabaseSpec()}
	observed, err := provider.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if observed.ProviderResourceID != "silent-snow-12345678" || observed.Status != managedpostgres.ProviderStatusPending || observed.Spec != request.Spec || postCount.Load() != 1 {
		t.Fatalf("observed = %+v, posts = %d", observed, postCount.Load())
	}
}

func TestProvisionRecoversAcceptedProjectWithoutSecondCreate(t *testing.T) {
	var postCount atomic.Int32
	provider := testProvider(t, readyProjectHandler(t, &postCount))
	request := managedpostgres.ProvisionRequest{ResourceID: "22222222-2222-2222-2222-222222222222", IdempotencyKey: "provision-22222222-2222-2222-2222-222222222222", Spec: testDatabaseSpec()}
	observed, err := provider.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision recovery: %v", err)
	}
	if observed.ProviderResourceID != "quiet-river-12345678" || observed.Status != managedpostgres.ProviderStatusReady || observed.Spec != request.Spec || postCount.Load() != 0 {
		t.Fatalf("recovered = %+v, posts = %d", observed, postCount.Load())
	}
}

func TestProvisionRecoversAfterAmbiguousCreateResponse(t *testing.T) {
	var mu sync.Mutex
	createdName := ""
	postCount := 0
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch request.URL.Path {
		case "/api/v2/projects":
			switch request.Method {
			case http.MethodGet:
				projects := []map[string]any{}
				if createdName != "" {
					projects = append(projects, map[string]any{"id": "quiet-river-12345678", "name": createdName})
				}
				writeResponse(t, writer, http.StatusOK, map[string]any{"projects": projects, "pagination": map[string]any{}})
			case http.MethodPost:
				postCount++
				var payload createProjectRequest
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Errorf("decode create: %v", err)
				}
				createdName = payload.Project.Name
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte("{"))
			default:
				t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
				writeResponse(t, writer, http.StatusMethodNotAllowed, nil)
			}
		case "/api/v2/projects/quiet-river-12345678":
			writeResponse(t, writer, http.StatusOK, map[string]any{"project": map[string]any{
				"id": "quiet-river-12345678", "name": createdName, "region_id": "aws-eu-central-1", "pg_version": 17,
				"history_retention_seconds": 86400, "settings": map[string]any{"quota": map[string]any{"logical_size_bytes": 10 << 30}},
			}})
		case "/api/v2/projects/quiet-river-12345678/branches":
			writeResponse(t, writer, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-main-123", "default": true, "current_state": "ready"}}})
		case "/api/v2/projects/quiet-river-12345678/endpoints":
			writeResponse(t, writer, http.StatusOK, map[string]any{"endpoints": []map[string]any{{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write", "current_state": "idle", "autoscaling_limit_min_cu": 0.25, "autoscaling_limit_max_cu": 2, "suspend_timeout_seconds": 300}}})
		case "/api/v2/projects/quiet-river-12345678/operations":
			writeResponse(t, writer, http.StatusOK, map[string]any{"operations": []map[string]any{{"id": "op-1", "status": "finished"}}, "pagination": map[string]any{}})
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
			writeResponse(t, writer, http.StatusNotFound, nil)
		}
	}))
	request := managedpostgres.ProvisionRequest{ResourceID: "33333333-3333-3333-3333-333333333333", IdempotencyKey: "provision-33333333-3333-3333-3333-333333333333", Spec: testDatabaseSpec()}
	first, err := provider.Provision(context.Background(), request)
	if err != nil || first.ProviderResourceID != "quiet-river-12345678" || first.Status != managedpostgres.ProviderStatusPending {
		t.Fatalf("ambiguous create recovery = %+v, %v", first, err)
	}
	observed, err := provider.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision recovery: %v", err)
	}
	if observed.ProviderResourceID != "quiet-river-12345678" || observed.Status != managedpostgres.ProviderStatusReady || observed.Spec != request.Spec || postCount != 1 {
		t.Fatalf("recovered = %+v, posts = %d", observed, postCount)
	}
}

func readyProjectHandler(t *testing.T, postCount *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/projects":
			if request.Method == http.MethodPost {
				postCount.Add(1)
			}
			name := request.URL.Query().Get("search")
			writeResponse(t, writer, http.StatusOK, map[string]any{"projects": []map[string]any{{"id": "quiet-river-12345678", "name": name}}, "pagination": map[string]any{}})
		case "/api/v2/projects/quiet-river-12345678":
			writeResponse(t, writer, http.StatusOK, map[string]any{"project": map[string]any{
				"id": "quiet-river-12345678", "name": "managed", "region_id": "aws-eu-central-1", "pg_version": 17,
				"history_retention_seconds": 86400, "settings": map[string]any{"quota": map[string]any{"logical_size_bytes": 10 << 30}},
			}})
		case "/api/v2/projects/quiet-river-12345678/branches":
			writeResponse(t, writer, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-main-123", "default": true, "current_state": "ready"}}})
		case "/api/v2/projects/quiet-river-12345678/endpoints":
			writeResponse(t, writer, http.StatusOK, map[string]any{"endpoints": []map[string]any{{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write", "current_state": "idle", "autoscaling_limit_min_cu": 0.25, "autoscaling_limit_max_cu": 2, "suspend_timeout_seconds": 300}}})
		case "/api/v2/projects/quiet-river-12345678/operations":
			writeResponse(t, writer, http.StatusOK, map[string]any{"operations": []map[string]any{{"id": "op-1", "status": "finished"}}, "pagination": map[string]any{}})
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
			writeResponse(t, writer, http.StatusNotFound, nil)
		}
	})
}

func TestProvisionRejectsAmbiguousRecoveryAndSanitizesErrors(t *testing.T) {
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := request.URL.Query().Get("search")
		writeResponse(t, writer, http.StatusOK, map[string]any{"projects": []map[string]any{{"id": "first-123", "name": name}, {"id": "second-123", "name": name}}, "pagination": map[string]any{}})
	}))
	_, err := provider.Provision(context.Background(), managedpostgres.ProvisionRequest{ResourceID: "database", IdempotencyKey: "provision-database", Spec: testDatabaseSpec()})
	if !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("ambiguous recovery error = %v", err)
	}

	provider = testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(t, writer, http.StatusInternalServerError, map[string]any{"message": "password=upstream-secret"})
	}))
	_, err = provider.Provision(context.Background(), managedpostgres.ProvisionRequest{ResourceID: "database", IdempotencyKey: "provision-database", Spec: testDatabaseSpec()})
	if !errors.Is(err, managedpostgres.ErrUnavailable) || strings.Contains(err.Error(), "upstream-secret") {
		t.Fatalf("unsanitized provider error = %v", err)
	}
}

func TestDeleteTreatsMissingProjectAsComplete(t *testing.T) {
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(t, writer, http.StatusNotFound, map[string]any{"message": "gone"})
	}))
	result, err := provider.Delete(context.Background(), managedpostgres.DeleteRequest{ProviderResourceID: "gone-project-123", IdempotencyKey: "delete-database"})
	if err != nil || !result.Done {
		t.Fatalf("Delete = %+v, %v", result, err)
	}
}

func TestDeleteDiscoversAcceptedProjectWhenProviderIDWasNotPersisted(t *testing.T) {
	var deleteCount atomic.Int32
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v2/projects":
			name := request.URL.Query().Get("search")
			writeResponse(t, writer, http.StatusOK, map[string]any{"projects": []map[string]any{{"id": "quiet-river-12345678", "name": name}}, "pagination": map[string]any{}})
		case request.Method == http.MethodDelete && request.URL.Path == "/api/v2/projects/quiet-river-12345678":
			deleteCount.Add(1)
			writeResponse(t, writer, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
			writeResponse(t, writer, http.StatusNotFound, nil)
		}
	}))
	result, err := provider.Delete(context.Background(), managedpostgres.DeleteRequest{ResourceID: "33333333-3333-3333-3333-333333333333", IdempotencyKey: "delete-database"})
	if err != nil || !result.Done || deleteCount.Load() != 1 {
		t.Fatalf("Delete = %+v, %v; deletes = %d", result, err, deleteCount.Load())
	}
}

func TestOperationStatusRequiresReadyResourcesAndFinishedOperations(t *testing.T) {
	tests := []struct {
		operations []operation
		ready      bool
		want       managedpostgres.ProviderStatus
	}{
		{[]operation{{Status: "finished"}}, true, managedpostgres.ProviderStatusReady},
		{[]operation{{Status: "running"}}, true, managedpostgres.ProviderStatusPending},
		{[]operation{{Status: "failed"}}, false, managedpostgres.ProviderStatusPending},
		{[]operation{{Status: "error"}}, false, managedpostgres.ProviderStatusFailed},
		{[]operation{{Status: "cancelled"}}, false, managedpostgres.ProviderStatusFailed},
	}
	for _, test := range tests {
		if got := operationStatus(test.operations, test.ready); got != test.want {
			t.Fatalf("operationStatus(%+v, %v) = %q, want %q", test.operations, test.ready, got, test.want)
		}
	}
}

func TestIssueCredentialsIsIdempotentAndRevokeDeletesRole(t *testing.T) {
	var mu sync.Mutex
	roleExists := false
	postCount := 0
	deleteCount := 0
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case request.URL.Path == "/api/v2/projects/quiet-river-123/branches":
			writeResponse(t, writer, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-main-123", "default": true, "current_state": "ready"}}})
		case strings.HasSuffix(request.URL.Path, "/roles") && request.Method == http.MethodPost:
			postCount++
			roleExists = true
			writeResponse(t, writer, http.StatusCreated, map[string]any{"role": map[string]any{"name": roleFromRequest(t, request)}, "operations": []map[string]any{{"id": "11111111-1111-1111-1111-111111111111", "status": "running"}}})
		case strings.Contains(request.URL.Path, "/roles/") && request.Method == http.MethodGet:
			if !roleExists {
				writeResponse(t, writer, http.StatusNotFound, map[string]any{"message": "missing"})
				return
			}
			writeResponse(t, writer, http.StatusOK, map[string]any{"role": map[string]any{"name": pathLast(request.URL.Path)}})
		case strings.Contains(request.URL.Path, "/roles/") && request.Method == http.MethodDelete:
			deleteCount++
			roleExists = false
			writer.WriteHeader(http.StatusNoContent)
		case strings.Contains(request.URL.Path, "/operations/"):
			writeResponse(t, writer, http.StatusOK, map[string]any{"operation": map[string]any{"id": pathLast(request.URL.Path), "status": "finished"}})
		case strings.HasSuffix(request.URL.Path, "/connection_uri"):
			host := "direct.db.example"
			if request.URL.Query().Get("pooled") == "true" {
				host = "pooler.db.example"
			}
			roleName := request.URL.Query().Get("role_name")
			uri := "postgres://" + url.UserPassword(roleName, "p@ss:word").String() + "@" + host + ":5432/gregale?sslmode=require"
			writeResponse(t, writer, http.StatusOK, map[string]any{"uri": uri})
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
			writeResponse(t, writer, http.StatusNotFound, nil)
		}
	}))
	request := managedpostgres.CredentialRequest{ProviderResourceID: "quiet-river-123", IdentityKey: "binding-123", Access: managedpostgres.CredentialReadWrite, IdempotencyKey: "credentials-binding-123-1"}
	first, err := provider.IssueCredentials(context.Background(), request)
	if err != nil {
		t.Fatalf("IssueCredentials: %v", err)
	}
	second, err := provider.IssueCredentials(context.Background(), request)
	if err != nil {
		t.Fatalf("IssueCredentials retry: %v", err)
	}
	if first.Username != second.Username || first.Password != "p@ss:word" || len(first.Endpoints) != 2 || first.Endpoints[0].Role != managedpostgres.EndpointPooled || postCount != 1 {
		t.Fatalf("credentials = %+v / %+v; posts=%d", first, second, postCount)
	}
	if err := provider.RevokeCredentials(context.Background(), request); err != nil {
		t.Fatalf("RevokeCredentials: %v", err)
	}
	if deleteCount != 1 {
		t.Fatalf("role deletes = %d", deleteCount)
	}
	readOnly := request
	readOnly.Access = managedpostgres.CredentialReadOnly
	if _, err := provider.IssueCredentials(context.Background(), readOnly); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("read-only error = %v", err)
	}
}

func roleFromRequest(t *testing.T, request *http.Request) string {
	t.Helper()
	var payload createRoleRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Errorf("decode role: %v", err)
	}
	return payload.Role.Name
}

func pathLast(path string) string {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	return parts[len(parts)-1]
}

func TestUsageNormalizesComputeAndNetworkMeters(t *testing.T) {
	var requests atomic.Int32
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		query := request.URL.Query()
		if query.Get("granularity") != "hourly" || query.Get("metrics") != consumptionMetrics || query.Get("org_id") != "org-gregale-12345678" {
			t.Errorf("usage query = %v", query)
		}
		writeResponse(t, writer, http.StatusOK, map[string]any{
			"projects": []map[string]any{{"project_id": "quiet-river-123", "periods": []map[string]any{{"consumption": []map[string]any{
				{"metrics": []map[string]any{{"metric_name": "compute_unit_seconds", "value": 10}, {"metric_name": "public_network_transfer_bytes", "value": 4}}},
				{"metrics": []map[string]any{{"metric_name": "compute_unit_seconds", "value": 5}, {"metric_name": "private_network_transfer_bytes", "value": 6}}},
			}}}}},
			"pagination": map[string]any{},
		})
	}))
	window := managedpostgres.UsageWindow{From: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
	usage, err := provider.Usage(context.Background(), "quiet-river-123", window)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if len(usage.Readings) != 2 || usage.Readings[0].Quantity != 15 || usage.Readings[1].Quantity != 10 {
		t.Fatalf("usage = %+v", usage)
	}
	window.From = window.From.Add(time.Minute)
	if _, err := provider.Usage(context.Background(), "quiet-river-123", window); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("unaligned usage error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("unsupported window contacted provider %d times", requests.Load())
	}
}
