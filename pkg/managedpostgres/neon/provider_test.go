package neon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

func TestComputeStateMapsNeonEndpointStates(t *testing.T) {
	tests := map[string]managedpostgres.ComputeState{
		"active":   managedpostgres.ComputeStateActive,
		"idle":     managedpostgres.ComputeStateSuspended,
		"starting": managedpostgres.ComputeStateWaking,
		"unknown":  managedpostgres.ComputeStateUnknown,
	}
	for input, want := range tests {
		if got := computeState(input); got != want {
			t.Errorf("computeState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestProbeDSNPrefersDirectEndpoint(t *testing.T) {
	dsn, err := probeDSN(managedpostgres.CredentialMaterial{
		Username: "gregale", Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{
			{Role: managedpostgres.EndpointPooled, Host: "pool.example", Port: 6432},
			{Role: managedpostgres.EndpointDirect, Host: "direct.example", Port: 5432},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "direct.example:5432" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("dsn = %q", dsn)
	}
	if parsed.User.Username() != "gregale" {
		t.Fatalf("dsn username = %q", parsed.User.Username())
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
	p.roles = &fakeCredentialRoles{}
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
	if err := capabilities.SupportsCredentialAccess(managedpostgres.CredentialReadWrite); err != nil {
		t.Fatalf("expected read-write binding support: %v", err)
	}
	if err := capabilities.SupportsCredentialAccess(managedpostgres.CredentialMigration); err != nil {
		t.Fatalf("migration capability: %v", err)
	}
	if err := capabilities.SupportsCredentialAccess(managedpostgres.CredentialReadOnly); err != nil {
		t.Fatalf("read-only binding support = %v", err)
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

func TestRestoreCreatesPointInTimeBranchWithOwnOpaqueResource(t *testing.T) {
	pointInTime := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v2/projects/quiet-river-12345678/branches" {
			t.Errorf("unexpected restore request: %s %s", request.Method, request.URL.String())
			writeResponse(t, writer, http.StatusNotFound, nil)
			return
		}
		switch request.Method {
		case http.MethodGet:
			writeResponse(t, writer, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-main-123", "name": "main", "default": true, "current_state": "ready"}}})
		case http.MethodPost:
			var payload createBranchRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode restore branch: %v", err)
			}
			if payload.Branch.ParentID != "br-main-123" || payload.Branch.ParentTimestamp != pointInTime.Format(time.RFC3339) || payload.Branch.InitSource != "parent-data" || len(payload.Endpoints) != 1 || payload.Endpoints[0].Type != "read_write" {
				t.Fatalf("restore payload = %+v", payload)
			}
			writeResponse(t, writer, http.StatusCreated, map[string]any{"branch": map[string]any{"id": "br-restore-123", "project_id": "quiet-river-12345678", "name": payload.Branch.Name, "parent_id": payload.Branch.ParentID, "parent_timestamp": payload.Branch.ParentTimestamp, "init_source": "parent-data", "current_state": "init"}, "operations": []map[string]any{{"id": "op-restore", "status": "running"}}})
		default:
			t.Errorf("unexpected restore method: %s", request.Method)
			writeResponse(t, writer, http.StatusMethodNotAllowed, nil)
		}
	}))
	request := managedpostgres.RestoreRequest{
		ResourceID: "44444444-4444-4444-4444-444444444444", SourceResourceID: "quiet-river-12345678",
		Spec: testDatabaseSpec(), PointInTime: pointInTime, IdempotencyKey: "restore-44444444-4444-4444-4444-444444444444",
	}
	observed, err := provider.Restore(context.Background(), request)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if observed.ProviderResourceID != "quiet-river-12345678/br-restore-123" || observed.Status != managedpostgres.ProviderStatusPending || observed.Spec != request.Spec {
		t.Fatalf("restore observed = %+v", observed)
	}
	if observed.RestoreLineage == nil || observed.RestoreLineage.SourceResourceID != "quiet-river-12345678/br-main-123" || !observed.RestoreLineage.PointInTime.Equal(pointInTime) {
		t.Fatalf("restore lineage = %+v", observed.RestoreLineage)
	}
}

func TestInspectRoutesRestoredResourceToBranch(t *testing.T) {
	provider := testProvider(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/projects/quiet-river-12345678":
			writeResponse(t, writer, http.StatusOK, map[string]any{"project": map[string]any{
				"id": "quiet-river-12345678", "region_id": "aws-eu-central-1", "pg_version": 17,
				"history_retention_seconds": 86400, "settings": map[string]any{"quota": map[string]any{"logical_size_bytes": 10 << 30}},
			}})
		case "/api/v2/projects/quiet-river-12345678/connection_uri":
			writeResponse(t, writer, http.StatusOK, map[string]any{"uri": "postgres://gregale_owner:secret@owner.example/gregale?sslmode=require"})
		case "/api/v2/projects/quiet-river-12345678/branches":
			writeResponse(t, writer, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-restore-123", "project_id": "quiet-river-12345678", "name": "restore", "parent_id": "br-main-123", "parent_timestamp": "2026-09-05T11:00:00Z", "init_source": "parent-data", "current_state": "ready"}}})
		case "/api/v2/projects/quiet-river-12345678/endpoints":
			writeResponse(t, writer, http.StatusOK, map[string]any{"endpoints": []map[string]any{{"id": "ep-restore-123", "branch_id": "br-restore-123", "type": "read_write", "current_state": "idle", "autoscaling_limit_min_cu": 0.25, "autoscaling_limit_max_cu": 2, "suspend_timeout_seconds": 300}}})
		case "/api/v2/projects/quiet-river-12345678/operations":
			writeResponse(t, writer, http.StatusOK, map[string]any{"operations": []map[string]any{{"id": "op-restore", "status": "finished"}}, "pagination": map[string]any{}})
		default:
			t.Errorf("unexpected inspect request: %s %s", request.Method, request.URL.String())
			writeResponse(t, writer, http.StatusNotFound, nil)
		}
	}))
	observed, err := provider.Inspect(context.Background(), "quiet-river-12345678/br-restore-123")
	if err != nil {
		t.Fatalf("Inspect restored resource: %v", err)
	}
	if observed.Status != managedpostgres.ProviderStatusReady || observed.ProviderResourceID != "quiet-river-12345678/br-restore-123" || observed.Spec != testDatabaseSpec() {
		t.Fatalf("inspected restored resource = %+v", observed)
	}
	if observed.RestoreLineage == nil || observed.RestoreLineage.SourceResourceID != "quiet-river-12345678/br-main-123" || observed.RestoreLineage.PointInTime.Format(time.RFC3339) != "2026-09-05T11:00:00Z" {
		t.Fatalf("inspected restore lineage = %+v", observed.RestoreLineage)
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
	deleteCount := 0
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/branches"):
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-main-123", "default": true}}})
		case strings.Contains(r.URL.Path, "/roles/") && r.Method == http.MethodDelete:
			deleteCount++
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/connection_uri"):
			host := "direct.db.example"
			if r.URL.Query().Get("pooled") == "true" {
				host = "pooler.db.example"
			}
			uri := "postgres://" + url.UserPassword(r.URL.Query().Get("role_name"), "p@ss:word").String() + "@" + host + ":5432/gregale?sslmode=require"
			writeResponse(t, w, http.StatusOK, map[string]any{"uri": uri})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	roles := provider.roles.(*fakeCredentialRoles)
	request := managedpostgres.CredentialRequest{ProviderResourceID: "quiet-river-123", IdentityKey: "binding-123", Access: managedpostgres.CredentialReadWrite, IdempotencyKey: "credentials-binding-123-1"}
	first, err := provider.IssueCredentials(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.IssueCredentials(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Username != second.Username || first.Username != provider.credentialRole(request).name || first.Password != "p@ss:word" || len(first.Endpoints) != 2 || roles.ensures != 2 {
		t.Fatalf("credential retry: username=%s ensures=%d", first.Username, roles.ensures)
	}
	if err := provider.RevokeCredentials(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if roles.revokes != 1 || deleteCount != 1 {
		t.Fatalf("revokes=%d legacy deletes=%d", roles.revokes, deleteCount)
	}
	request.Access = managedpostgres.CredentialMigration
	migration, err := provider.IssueCredentials(context.Background(), request)
	if err != nil || !strings.HasPrefix(migration.Username, "gregale_mig_") || migration.Username == first.Username || len(migration.Endpoints) != 1 || migration.Endpoints[0].Role != managedpostgres.EndpointDirect {
		t.Fatalf("migration separation: %v", err)
	}
	request.Access = managedpostgres.CredentialReadOnly
	reader, err := provider.IssueCredentials(context.Background(), request)
	if err != nil || !strings.HasPrefix(reader.Username, "gregale_ro_") || reader.Username == first.Username || reader.Username == migration.Username || len(reader.Endpoints) != 2 {
		t.Fatalf("read-only: %v", err)
	}
}

func TestCredentialIssuanceFailsClosedWithoutReturningOwnerOrProviderErrors(t *testing.T) {
	for _, kind := range []string{"SQL failure", "SQL conflict", "owner response", "wrong database"} {
		t.Run(kind, func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/branches") {
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-main", "default": true}}})
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/connection_uri") {
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				name := r.URL.Query().Get("role_name")
				database := "gregale"
				if kind == "owner response" {
					name = ownerLogin
				}
				if kind == "wrong database" {
					database = "other"
				}
				writeResponse(t, w, http.StatusOK, map[string]any{"uri": "postgres://" + url.UserPassword(name, "private-password").String() + "@db.example/" + database + "?sslmode=require"})
			}))
			roles := provider.roles.(*fakeCredentialRoles)
			if kind == "SQL failure" {
				roles.err = errors.New("private-password")
			}
			if kind == "SQL conflict" {
				roles.err = fmt.Errorf("private-password: %w", managedpostgres.ErrConflict)
			}
			material, err := provider.IssueCredentials(context.Background(), managedpostgres.CredentialRequest{ProviderResourceID: "project-123", IdentityKey: "binding", IdempotencyKey: "issue", Access: managedpostgres.CredentialReadWrite})
			if err == nil || strings.Contains(err.Error(), "private-password") || material.Password != "" || material.Username != "" {
				t.Fatalf("unsafe issuance error=%v username=%s", err, material.Username)
			}
		})
	}
}

func TestRestoredBranchReadinessFailsClosedWhenCredentialIsolationFails(t *testing.T) {
	var posts atomic.Int32
	base := readyProjectHandler(t, &posts)
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/connection_uri") {
			writeResponse(t, w, http.StatusOK, map[string]any{"uri": "postgres://gregale_owner:secret@db.example/gregale?sslmode=require"})
			return
		}
		base.ServeHTTP(w, r)
	}))
	provider.roles.(*fakeCredentialRoles).err = managedpostgres.ErrUnavailable
	// The helper's default branch is the requested restored branch for this check.
	observed, err := provider.Inspect(context.Background(), "quiet-river-12345678/br-main-123")
	if !errors.Is(err, managedpostgres.ErrUnavailable) || observed.Status == managedpostgres.ProviderStatusReady {
		t.Fatalf("unsafe restore readiness=%s err=%v", observed.Status, err)
	}
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
			"projects": []map[string]any{{"project_id": "quiet-river-123", "periods": []map[string]any{{"period_id": "period-a", "period_start": "2026-09-01T00:00:00Z", "consumption": []map[string]any{
				{"timeframe_start": "2026-09-05T10:00:00Z", "timeframe_end": "2026-09-05T11:00:00Z", "metrics": []map[string]any{{"metric_name": "compute_unit_seconds", "value": 10}, {"metric_name": "root_branch_bytes_month", "value": 2}, {"metric_name": "instant_restore_bytes_month", "value": 4}, {"metric_name": "public_network_transfer_bytes", "value": 4}}},
				{"timeframe_start": "2026-09-05T11:00:00Z", "timeframe_end": "2026-09-05T12:00:00Z", "metrics": []map[string]any{{"metric_name": "compute_unit_seconds", "value": 5}, {"metric_name": "child_branch_bytes_month", "value": 3}, {"metric_name": "snapshot_storage_bytes_month", "value": 5}, {"metric_name": "private_network_transfer_bytes", "value": 6}}},
			}}}}},
			"pagination": map[string]any{},
		})
	}))
	window := managedpostgres.UsageWindow{From: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
	usage, err := provider.Usage(context.Background(), "quiet-river-123", window)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if len(usage.Readings) != 4 || usage.Readings[0].Quantity != 15 || usage.Readings[1].Quantity != 5*744*int64(time.Hour/time.Second) || usage.Readings[2].Quantity != 9*744*int64(time.Hour/time.Second) || usage.Readings[3].Quantity != 10 {
		t.Fatalf("usage = %+v", usage)
	}
	window.From = window.From.Add(time.Minute)
	if _, err := provider.Usage(context.Background(), "quiet-river-123", window); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("unaligned usage error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("unsupported window contacted provider %d times", requests.Load())
	}
	if _, err := provider.Usage(context.Background(), "quiet-river-123/br-restore-123", managedpostgres.UsageWindow{From: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("branch usage error = %v", err)
	}
}

func TestByteMonthsToSecondsRejectsOverflow(t *testing.T) {
	if got, err := byteMonthsToSeconds(2); err != nil || got != 2*744*int64(time.Hour/time.Second) {
		t.Fatalf("byte-months conversion = %d, %v", got, err)
	}
	if _, err := byteMonthsToSeconds(math.MaxInt64/(744*int64(time.Hour/time.Second)) + 1); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("overflow conversion = %v, want ErrUnavailable", err)
	}
}

func TestDeleteBranchRequiresCompletedOperationsOrConfirmedAbsence(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		branchStatus int
		wantDone     bool
		wantErr      error
	}{
		{name: "running", status: http.StatusOK, body: `{"operations":[{"id":"op-delete","status":"running"}]}`},
		{name: "queued", status: http.StatusOK, body: `{"operations":[{"id":"op-delete","status":"scheduling"}]}`},
		{name: "finished", status: http.StatusOK, body: `{"operations":[{"id":"op-delete","status":"finished"}]}`, wantDone: true},
		{name: "mixed", status: http.StatusOK, body: `{"operations":[{"id":"op-one","status":"finished"},{"id":"op-two","status":"running"}]}`},
		{name: "failed", status: http.StatusOK, body: `{"operations":[{"id":"op-delete","status":"error"}]}`, wantErr: managedpostgres.ErrUnavailable},
		{name: "cancelled", status: http.StatusOK, body: `{"operations":[{"id":"op-delete","status":"cancelled"}]}`, wantErr: managedpostgres.ErrUnavailable},
		{name: "unidentified", status: http.StatusOK, body: `{"operations":[{"status":"finished"}]}`},
		{name: "already missing", status: http.StatusNotFound, wantDone: true},
		{name: "empty operations still exists", status: http.StatusOK, body: `{}`, branchStatus: http.StatusOK},
		{name: "empty operations absent", status: http.StatusOK, body: `{}`, branchStatus: http.StatusNotFound, wantDone: true},
		{name: "no content still exists", status: http.StatusNoContent, branchStatus: http.StatusOK},
		{name: "no content absent", status: http.StatusNoContent, branchStatus: http.StatusNotFound, wantDone: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/projects/project-123/branches/br-restore" {
					t.Errorf("path = %s", r.URL.Path)
				}
				if r.Method == http.MethodGet {
					w.WriteHeader(test.branchStatus)
					_, _ = w.Write([]byte(`{"branch":{"id":"br-restore"}}`))
					return
				}
				if r.Method != http.MethodDelete {
					t.Errorf("method = %s", r.Method)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			result, err := provider.Delete(context.Background(), managedpostgres.DeleteRequest{ProviderResourceID: "project-123/br-restore", IdempotencyKey: "delete"})
			if result.Done != test.wantDone || !errors.Is(err, test.wantErr) {
				t.Fatalf("delete = %+v, %v", result, err)
			}
		})
	}
}
