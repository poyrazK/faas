// End-to-end coverage for the general-path managed PostgreSQL deployment
// seam. The provider adapter is configured normally, while the catalog is
// seeded with a ready database/binding so the test never calls a vendor API.
//
// This pins the customer-visible contract that was previously only covered
// by unit tests: manifest resolution happens before project mutation, ready
// bindings are reused on re-apply, credentials stay scoped to the target,
// and account boundaries remain enforced.
package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestE2E_ManagedPostgres_ManifestReapplyPreservesScopedBinding(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	extraEnv, backend := managedPostgresE2EEnv(t)
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, extraEnv)
	key := h.SeedAccount(context.Background(), api.PlanPro, "managed-postgres-manifest")
	ctx := context.Background()
	account, err := state.NewPgStore(pool).AccountByEmail(ctx, "e2e+pro+managed-postgres-manifest@test.example")
	if err != nil {
		t.Fatalf("account: %v", err)
	}

	first := applyProjectMultipartWithOptions(t, h, key, "managed-orders", "", "", true, managedPostgresSource(t, false))
	appID := managedPostgresAppID(t, first, "api")

	database := seedManagedPostgresDatabase(t, pool, account.ID, backend, "orders", true)
	binding := seedManagedPostgresBinding(t, pool, account.ID, database.ID, appID)
	manifestSource := managedPostgresSource(t, true)

	second := applyProjectMultipartWithOptions(t, h, key, "managed-orders", "", "", true, manifestSource)
	if second.ProjectID != first.ProjectID {
		t.Fatalf("re-apply project_id=%q want %q", second.ProjectID, first.ProjectID)
	}
	if got := managedPostgresAppID(t, second, "api"); got != appID {
		t.Fatalf("re-apply api app_id=%q want %q", got, appID)
	}

	// A second identical apply must reuse the existing ready binding rather
	// than reserve a duplicate target or advance its credential generation.
	third := applyProjectMultipartWithOptions(t, h, key, "managed-orders", "", "", true, manifestSource)
	if got := managedPostgresAppID(t, third, "api"); got != appID {
		t.Fatalf("third apply api app_id=%q want %q", got, appID)
	}

	var databases api.ManagedPostgresDatabaseList
	raw, status := doReq(t, h, key, http.MethodGet, "/v1/postgres/databases", nil)
	if status != http.StatusOK {
		t.Fatalf("list databases status=%d: %s", status, raw)
	}
	if err := json.Unmarshal(raw, &databases); err != nil {
		t.Fatalf("decode databases: %v", err)
	}
	if len(databases.Items) != 1 || databases.Items[0].ID != database.ID || databases.Items[0].State != string(managedpostgres.StateReady) {
		t.Fatalf("databases=%+v", databases.Items)
	}
	if strings.Contains(string(raw), "provider-orders") || strings.Contains(string(raw), backend.ID) {
		t.Fatalf("database response leaked provider identity: %s", raw)
	}

	var bindings api.ManagedPostgresBindingList
	raw, status = doReq(t, h, key, http.MethodGet, "/v1/postgres/databases/"+database.ID+"/bindings", nil)
	if status != http.StatusOK {
		t.Fatalf("list bindings status=%d: %s", status, raw)
	}
	if err := json.Unmarshal(raw, &bindings); err != nil {
		t.Fatalf("decode bindings: %v", err)
	}
	if len(bindings.Items) != 1 {
		t.Fatalf("bindings=%+v, want one idempotent binding", bindings.Items)
	}
	gotBinding := bindings.Items[0]
	if gotBinding.ID != binding.ID || gotBinding.AppID != appID || gotBinding.Scope != "production" ||
		gotBinding.EnvironmentKey != "DATABASE_URL" || gotBinding.Access != string(managedpostgres.CredentialReadWrite) ||
		gotBinding.State != string(managedpostgres.BindingStateReady) || gotBinding.CredentialGeneration != 1 {
		t.Fatalf("binding=%+v", gotBinding)
	}
	if strings.Contains(string(raw), "managed-e2e-") || strings.Contains(string(raw), "provider-role-e2e") || strings.Contains(string(raw), "sealed-e2e-credential") {
		t.Fatalf("binding response leaked credential/provider identity: %s", raw)
	}

	secret, err := state.NewPgStore(pool).GetAppSecretInScope(ctx, account.ID, appID, "production", "DATABASE_URL")
	if err != nil {
		t.Fatalf("managed secret: %v", err)
	}
	if secret.ManagedPostgresBindingID != binding.ID || secret.ManagedCredentialRef == "" || secret.ManagedCredentialGeneration != 1 || len(secret.Ciphertext) == 0 {
		t.Fatalf("managed secret ownership=%+v", secret)
	}
}

func TestE2E_ManagedPostgres_ManifestFailsClosedAndIsolatesTenants(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	extraEnv, backend := managedPostgresE2EEnv(t)
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, extraEnv)
	key := h.SeedAccount(context.Background(), api.PlanPro, "managed-postgres-fail-closed")
	otherKey := h.SeedAccount(context.Background(), api.PlanPro, "managed-postgres-fail-closed-other")
	ctx := context.Background()
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(ctx, "e2e+pro+managed-postgres-fail-closed@test.example")
	if err != nil {
		t.Fatalf("account: %v", err)
	}

	database := seedManagedPostgresDatabase(t, pool, account.ID, backend, "orders", true)
	pending := seedManagedPostgresDatabase(t, pool, account.ID, backend, "pending", false)

	assertManagedPostgresApplyProblem(t, h, key, "missing-managed-db", "missing", http.StatusNotFound, "managed_postgres_not_found")
	assertManagedPostgresProjectAbsent(t, pool, account.ID, "missing-managed-db")

	assertManagedPostgresApplyProblem(t, h, key, "pending-managed-db", pending.Name, http.StatusConflict, "managed_postgres_not_ready")
	assertManagedPostgresProjectAbsent(t, pool, account.ID, "pending-managed-db")

	assertProblem(t, h, otherKey, http.MethodGet, "/v1/postgres/databases/"+database.ID, nil, http.StatusNotFound, "managed_postgres_not_found")
}

func managedPostgresE2EEnv(t *testing.T) ([]string, managedpostgres.Backend) {
	t.Helper()
	config := managedpostgres.Config{
		DefaultRegion:          "eu-central-1",
		Defaults:               map[string]string{"eu-central-1": "e2e-neon"},
		MaxDatabasesPerAccount: 3,
		ProvisioningEnabled:    true,
		Backends: []managedpostgres.BackendConfig{{
			ID: "e2e-neon", Driver: "neon", Region: "eu-central-1", Namespace: "org-e2e-test",
			Settings:  map[string]string{"region_id": "aws-eu-central-1", "database_name": "gregale", "max_storage_bytes": "1073741824", "max_restore_window_seconds": "0"},
			SecretEnv: map[string]string{"api-key": "FAAS_E2E_NEON_API_KEY"},
		}},
	}
	getenv := func(name string) string {
		if name == "FAAS_E2E_NEON_API_KEY" {
			return "e2e-provider-key"
		}
		return ""
	}
	registry, err := managedpostgres.NewRegistry(config, getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil {
		t.Fatalf("managed postgres registry: %v", err)
	}
	backend, err := registry.Default("eu-central-1")
	if err != nil {
		t.Fatalf("managed postgres backend: %v", err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("managed postgres config: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "managed-postgres.json")
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatalf("write managed postgres config: %v", err)
	}
	return []string{
		"FAAS_MANAGED_POSTGRES_CONFIG=" + configPath,
		"FAAS_E2E_NEON_API_KEY=e2e-provider-key",
		"FAAS_ENVIRONMENT=staging",
		"FAAS_MANAGED_POSTGRES_QUALIFIED=true",
		"FAAS_MANAGED_POSTGRES_QUALIFIED_BACKEND=" + backend.ID,
		"FAAS_MANAGED_POSTGRES_QUALIFIED_FINGERPRINT=" + backend.Fingerprint,
		"FAAS_MANAGED_POSTGRES_QUALIFIED_UNTIL=" + time.Now().UTC().Add(2*time.Hour).Format(time.RFC3339),
	}, backend
}

func managedPostgresSource(t *testing.T, withManifest bool) []byte {
	t.Helper()
	database := ""
	if withManifest {
		database = "orders"
	}
	return managedPostgresSourceWithDatabase(t, database)
}

func managedPostgresSourceWithDatabase(t *testing.T, database string) []byte {
	t.Helper()
	entries := []struct {
		name, body string
	}{
		{"gregale-managed-postgres/docker-compose.yml", "services:\n  api:\n    build:\n      context: .\n"},
		{"gregale-managed-postgres/Dockerfile.api", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		{"gregale-managed-postgres/index.api.js", "exports.handler = () => 1;\n"},
	}
	if database != "" {
		entries = append(entries, struct{ name, body string }{
			"gregale-managed-postgres/gregale.yaml",
			"databases:\n  - database: " + database + "\n    app: api\n    scope: production\n    env: DATABASE_URL\n    access: read_write\n",
		})
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("tar header %s: %v", entry.name, err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatalf("tar write %s: %v", entry.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func managedPostgresAppID(t *testing.T, response api.ApplyResponse, slug string) string {
	t.Helper()
	for _, app := range response.Apps {
		if app.Slug == slug {
			return app.ID
		}
	}
	t.Fatalf("apply response has no %q app: %+v", slug, response.Apps)
	return ""
}

func seedManagedPostgresDatabase(t *testing.T, pool *pgxpool.Pool, accountID string, backend managedpostgres.Backend, name string, ready bool) managedpostgres.Database {
	t.Helper()
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("managed postgres store: %v", err)
	}
	now := time.Now().UTC()
	retryAt := now.Add(24 * time.Hour)
	if ready {
		retryAt = now.Add(-time.Second)
	}
	database, created, err := store.Reserve(context.Background(), managedpostgres.Database{
		ID: uuid.NewString(), AccountID: accountID, Name: name,
		Spec: managedpostgres.Spec{
			Region: "eu-central-1", PostgresMajor: 16,
			Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
			StorageLimitBytes: 1 << 30,
		},
		BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		State: managedpostgres.StateProvisioning, DesiredGeneration: 1,
		RetryAt: retryAt, CreatedAt: now, UpdatedAt: now,
	}, 3)
	if err != nil {
		t.Fatalf("reserve managed postgres database %q: %v", name, err)
	}
	if !created {
		t.Fatalf("managed postgres database %q was unexpectedly reused", name)
	}
	if !ready {
		return database
	}
	claimAt := now.Add(2 * time.Second)
	claimed, err := store.Claim(context.Background(), accountID, database.ID, "database-lease-"+database.ID, managedpostgres.StateProvisioning, claimAt, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("claim managed postgres database %q: %v", name, err)
	}
	providerAt := now.Add(3 * time.Second)
	if err := store.RecordProviderResource(context.Background(), database.ID, claimed.LeaseToken, "provider-"+name, providerAt); err != nil {
		t.Fatalf("record provider resource %q: %v", name, err)
	}
	readyDatabase, err := store.FinishProvision(context.Background(), database.ID, claimed.LeaseToken, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("finish managed postgres database %q: %v", name, err)
	}
	return readyDatabase
}

func assertManagedPostgresApplyProblem(t *testing.T, h *e2etest.Harness, key, slug, database string, wantStatus int, wantCode string) {
	t.Helper()
	raw, status := managedPostgresApplyRaw(t, h, key, slug, managedPostgresSourceWithDatabase(t, database))
	if status != wantStatus {
		t.Fatalf("apply %q status=%d want %d: %s", slug, status, wantStatus, raw)
	}
	var problem api.Problem
	if err := json.Unmarshal(raw, &problem); err != nil {
		t.Fatalf("decode apply problem: %v (body=%s)", err, raw)
	}
	if problem.Code != wantCode {
		t.Fatalf("apply %q code=%q want %q: %s", slug, problem.Code, wantCode, raw)
	}
}

func managedPostgresApplyRaw(t *testing.T, h *e2etest.Harness, key, slug string, body []byte) ([]byte, int) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	source, err := mw.CreateFormFile("source", "fixture.tar.gz")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := source.Write(body); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := mw.WriteField("project_slug", slug); err != nil {
		t.Fatalf("write slug: %v", err)
	}
	if err := mw.WriteField("no_triggers", "true"); err != nil {
		t.Fatalf("write no_triggers: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.APIDURL+"/v1/projects", &buf)
	if err != nil {
		t.Fatalf("new apply request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("apply request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read apply response: %v", err)
	}
	return raw, resp.StatusCode
}

func assertManagedPostgresProjectAbsent(t *testing.T, pool *pgxpool.Pool, accountID, slug string) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `select count(*) from projects where account_id = $1 and slug = $2`, accountID, slug).Scan(&count); err != nil {
		t.Fatalf("project count %q: %v", slug, err)
	}
	if count != 0 {
		t.Fatalf("project %q exists after rejected manifest", slug)
	}
}

func seedManagedPostgresBinding(t *testing.T, pool *pgxpool.Pool, accountID, databaseID, appID string) managedpostgres.Binding {
	t.Helper()
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("managed postgres store: %v", err)
	}
	now := time.Now().UTC()
	binding, created, err := store.ReserveBinding(context.Background(), managedpostgres.Binding{
		ID: uuid.NewString(), AccountID: accountID, DatabaseID: databaseID, AppID: appID,
		Scope: "production", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite,
		CredentialGeneration: 1, State: managedpostgres.BindingStateProvisioning,
		RetryAt: now.Add(-time.Second), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("reserve managed postgres binding: %v", err)
	}
	if !created {
		t.Fatalf("managed postgres binding was unexpectedly reused")
	}
	claimAt := now.Add(2 * time.Second)
	claimed, err := store.ClaimBinding(context.Background(), accountID, binding.ID, "binding-lease-"+binding.ID, managedpostgres.BindingStateProvisioning, claimAt, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("claim managed postgres binding: %v", err)
	}
	credentialRef := "managed-e2e-" + binding.ID
	if err := state.NewPgStore(pool).PutManagedPostgresSecret(context.Background(), state.AppSecret{
		AccountID: accountID, AppID: appID, Scope: "production", Key: "DATABASE_URL",
		Ciphertext: []byte("sealed-e2e-credential"), Kid: "age1e2e",
		ManagedPostgresBindingID: binding.ID, ManagedCredentialRef: credentialRef, ManagedCredentialGeneration: 1,
	}); err != nil {
		t.Fatalf("seed managed postgres secret: %v", err)
	}
	readyBinding, err := store.FinishBindingProvision(context.Background(), binding.ID, claimed.LeaseToken, "provider-role-e2e", credentialRef, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("finish managed postgres binding: %v", err)
	}
	return readyBinding
}
